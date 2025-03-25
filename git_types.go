package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gosimple/hashdir"
	"github.com/schollz/progressbar/v3"
)

const (
	SPACE byte   = 32
	NUL   byte   = 0
	GIT   string = ".git"
)

type Edge struct {
	Src  string `json:"src"`
	Dest string `json:"dest"`
}

type Head struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type Branch struct {
	Name   string `json:"name"`
	Commit string `json:"commit"`
}

type Object struct {
	Type     string `json:"type"`
	Size     string `json:"size"`
	Location string `json:"location"`
	Name     string `json:"name"`
	Content  []byte `json:"content"`
}

type Blob struct {
	Content string `json:"content"`
	Size    int    `json:"size"`
}

type TreeEntry struct {
	Mode string `json:"mode"`
	Name string `json:"name"`
	Hash string `json:"hash"`
}

type User struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type Commit struct {
	Tree       string    `json:"tree"`
	Parents    []string  `json:"parents"`
	Author     User      `json:"author"`
	Committer  User      `json:"committer"`
	Message    string    `json:"message"`
	CommitTime time.Time `json:"commitTime"`
	AuthorTime time.Time `json:"authorTime"`
}

type Repo struct {
	Location string
	Objects  map[string]*Object
	Checksum string
}

func newObject(objectPath string) *Object {
	data := getBytes(objectPath, true)
	objType, spaceIndex, err := getType(data)
	if err != nil {
		slog.Warn(err.Error())
	}
	size, contentStart, err := getSize(spaceIndex, data)
	if err != nil {
		slog.Warn(err.Error())
	}
	return &Object{objType, size, objectPath, getObjectName(objectPath), data[contentStart:]}
}

func (obj *Object) toJson() []byte {
	switch obj.Type {
	case "tree":
		tree, err := json.MarshalIndent(map[string][]TreeEntry{"entries": *parseTree(obj)}, "", TAB)
		if err != nil {
			log.Fatal(err)
		}
		return tree
	case "commit":
		commit, err := json.MarshalIndent(parseCommit(obj), "", TAB)
		if err != nil {
			log.Fatal(err)
		}
		return commit
	case "blob":
		blob, err := json.MarshalIndent(parseBlob(obj), "", TAB)
		if err != nil {
			log.Fatal(err)
		}
		return blob
	default:
		slog.Warn(fmt.Sprintf("Could not convert object, %v, to json", obj.Type))
		return []byte("{}")
	}
}

func newRepo(location string) *Repo {
	objects := getObjects(gitDir(location) + "/objects")
	dirHash, err := hashdir.Make(gitDir(location), "md5")
	if err != nil {
		log.Fatal(err)
	}
	return &Repo{
		Location: location,
		Objects:  objects,
		Checksum: dirHash,
	}
}

func (r *Repo) changed() bool {
	dirHash, err := hashdir.Make(gitDir(r.Location), "md5")
	if err != nil {
		log.Fatal(err)
	}
	if r.Checksum != dirHash {
		r.Checksum = dirHash
		return true
	}
	return false
}

func (r *Repo) getObject(name string) (*Object, error) {
	obj, ok := r.Objects[name]
	if ok {
		return obj, nil
	}
	return nil, fmt.Errorf("Object, %v, doesn't seem to exist in the repo", name)
}

func (r *Repo) toJsonGraph() []byte {

	type Data struct {
		Nodes []map[string]any
		Edges []Edge
	}

	getNodesAndEdges := func(obj *Object) Data {
		edges := []Edge{}
		nodes := []map[string]any{}
		var objMap map[string]json.RawMessage

		err := json.Unmarshal(obj.toJson(), &objMap)
		if err != nil {
			log.Fatal(err)
		}
		nodes = append(nodes, map[string]any{"name": obj.Name, "type": obj.Type, "object": objMap})
		switch obj.Type {
		case "commit":
			commit := parseCommit(obj)
			// commit edges to parents
			for _, p := range commit.Parents {
				edges = append(edges, Edge{Src: obj.Name, Dest: p})
			}
			// commit edge to tree
			edges = append(edges, Edge{Src: obj.Name, Dest: commit.Tree})
		case "tree":
			entries := *parseTree(obj)
			// tree to blob edges
			for _, entry := range entries {
				edges = append(edges, Edge{Src: obj.Name, Dest: entry.Hash})
			}
		}
		return Data{Edges: edges, Nodes: nodes}
	}

	edges := []Edge{}
	nodes := []map[string]any{}
	for d := range parallelWork(slices.Collect(maps.Values(r.Objects)), getNodesAndEdges) {
		edges = append(edges, d.Edges...)
		nodes = append(nodes, d.Nodes...)
	}

	// add refs/branches
	head := r.head()
	nodes = append(nodes, map[string]any{"name": "HEAD", "type": "ref", "object": head})
	edges = append(edges, Edge{Src: "HEAD", Dest: filepath.Base(head.Value)})
	for _, b := range r.branches() {
		nodes = append(nodes, map[string]any{"name": b.Name, "type": "ref", "object": b})
		edges = append(edges, Edge{Src: b.Name, Dest: b.Commit})
	}

	repoGraph, err := json.MarshalIndent(map[string]any{"nodes": nodes, "edges": edges}, "", TAB)
	if err != nil {
		log.Fatal(err)
	}
	return repoGraph
}

func (r *Repo) toSQLite(path string) {
	slog.Info(fmt.Sprintf("Removing existing sqlite database at: %v...", path))
	os.Remove(path)

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	execSql(db, `CREATE TABLE objects (name text primary key, type text, object jsonb);`)
	execSql(db, `CREATE TABLE edges (src text, dest text);`)
	// these two commands allow for concurrent writes without encountering "database is locked" errors.
	execSql(db, "PRAGMA journal_mode = WAL;")
	execSql(db, "PRAGMA synchronous = normal;")

	objsStmt, err := db.Prepare("INSERT INTO objects(name, type, object) values(?, ?, ?)")
	if err != nil {
		log.Fatal(err)
	}
	edgesStmt, err := db.Prepare("INSERT INTO edges(src, dest) values(?, ?)")
	if err != nil {
		log.Fatal(err)
	}
	defer objsStmt.Close()
	defer edgesStmt.Close()

	insertObjectToTables := func(obj *Object) int {
		_, err = objsStmt.Exec(obj.Name, obj.Type, obj.toJson())
		if err != nil {
			log.Fatal(err)
		}
		switch obj.Type {
		case "commit":
			commit := parseCommit(obj)
			// commit edges to parents
			for _, p := range commit.Parents {
				_, err = edgesStmt.Exec(obj.Name, p)
				if err != nil {
					log.Fatal(err)
				}
			}
			// commit edge to tree
			_, err = edgesStmt.Exec(obj.Name, commit.Tree)
			if err != nil {
				log.Fatal(err)
			}
		case "tree":
			entries := *parseTree(obj)
			// tree to blob edges
			for _, entry := range entries {
				_, err = edgesStmt.Exec(obj.Name, entry.Hash)
				if err != nil {
					log.Fatal(err)
				}
			}

		}
		return 1
	}

	bar := progressbar.Default(int64(len(r.Objects)))
	for n := range parallelWork(slices.Collect(maps.Values(r.Objects)), insertObjectToTables) {
		bar.Add(n)
	}
}

func (r *Repo) refresh() {
	objects := getObjects(r.Location)
	r.Objects = objects
}

func (r *Repo) head() Head {
	bytes, err := os.ReadFile(gitDir(r.Location) + "/HEAD")
	if err != nil {
		log.Fatal(err)
	}
	var type_ string
	var value string
	arr := strings.Split(string(bytes), ":")
	if len(arr) > 1 {
		type_ = strings.TrimSpace(arr[0])
		value = strings.TrimSpace(arr[1])
		// detached head state. The content should just be a commit hash
	} else {
		type_ = "detached"
		value = strings.TrimSpace(arr[0])
	}
	return Head{Type: type_, Value: value}
}

func (r *Repo) currBranch() Branch {
	head := r.head()
	return newBranch(r.Location + fmt.Sprintf("/%s/", GIT) + head.Value)
}

func (r *Repo) currCommit() Commit {
	branch := r.currBranch()
	obj, err := r.getObject(branch.Commit)
	if err != nil {
		log.Fatal(err)
	}
	return parseCommit(obj)
}

func (r *Repo) branches() []Branch {
	branches := []Branch{}
	filepath.WalkDir(r.Location+fmt.Sprintf("/%s/refs/heads", GIT), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			log.Fatal(err)
		}
		if !d.IsDir() {
			branches = append(branches, newBranch(path))
		}
		return nil
	})
	return branches
}
