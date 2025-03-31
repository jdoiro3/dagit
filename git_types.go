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
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/gosimple/hashdir"
	"github.com/schollz/progressbar/v3"
)

const (
	space byte   = 32
	nul   byte   = 0
	git   string = ".git"
	tab   string = "    "
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

func NewObject(objectPath string) *Object {
	data := getBytes(objectPath, true)
	objType, spaceIndex, err := GetType(data)
	if err != nil {
		slog.Warn(err.Error())
	}
	size, contentStart, err := GetSize(spaceIndex, data)
	if err != nil {
		slog.Warn(err.Error())
	}
	return &Object{objType, size, objectPath, getObjectName(objectPath), data[contentStart:]}
}

func (obj *Object) ToJson() []byte {
	switch obj.Type {
	case "tree":
		tree, err := json.MarshalIndent(map[string][]*TreeEntry{"entries": ParseTree(obj)}, "", tab)
		if err != nil {
			log.Fatal(err)
		}
		return tree
	case "commit":
		commit, err := json.MarshalIndent(*ParseCommit(obj), "", tab)
		if err != nil {
			log.Fatal(err)
		}
		return commit
	case "blob":
		blob, err := json.MarshalIndent(*ParseBlob(obj), "", tab)
		if err != nil {
			log.Fatal(err)
		}
		return blob
	default:
		slog.Warn(fmt.Sprintf("Could not convert object, %v, to json", obj.Type))
		return []byte("{}")
	}
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

func NewRepo(location string) *Repo {
	objects := GetObjects(gitDir(location) + "/objects")
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

func (r *Repo) Changed() bool {
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

func (r *Repo) GetCommits(ascending bool) []*Object {
	var commits []*Object
	for _, obj := range r.Objects {
		if obj.Type == "commit" {
			commits = append(commits, obj)
		}
	}
	sort.Slice(commits, func(i, j int) bool {
		if ascending {
			return ParseCommit(commits[i]).CommitTime.Before(ParseCommit(commits[j]).CommitTime)
		}
		return ParseCommit(commits[i]).CommitTime.After(ParseCommit(commits[j]).CommitTime)
	})
	return commits
}

func (r *Repo) FindFirstInstanceOfBlob(name string, commits []*Object) (*Object, *Blob, error) {
	for _, c := range commits {
		tree := ParseTree(r.Objects[ParseCommit(c).Tree])
		for _, entry := range tree {
			if entry.Hash == name {
				return c, ParseBlob(r.Objects[name]), nil
			}
		}
	}
	return nil, nil, fmt.Errorf("could not find blob instance: %v", name)
}

// Only returns the commit if it's not an internal commit
func (r *Repo) GetTreeCommit(name string, commits []*Object) string {
	obj := r.Objects[name]
	if obj.Type == "tree" {
		for _, o := range commits {
			c := ParseCommit(o)
			if c.Tree == obj.Name {
				return o.Name
			}
		}
	}
	return ""
}

func (r *Repo) GetObject(name string) (*Object, error) {
	obj, ok := r.Objects[name]
	if ok {
		return obj, nil
	}
	return nil, fmt.Errorf("Object, %v, doesn't seem to exist in the repo", name)
}

func (r *Repo) ToJsonGraph() []byte {

	type Input struct {
		Commits []*Object
		Obj     *Object
	}

	type Data struct {
		Nodes []map[string]any
		Edges []Edge
	}

	getNodesAndEdges := func(input *Input) Data {
		obj := input.Obj
		commits := input.Commits
		edges := []Edge{}
		nodes := []map[string]any{}
		var objMap map[string]json.RawMessage

		err := json.Unmarshal(obj.ToJson(), &objMap)
		if err != nil {
			log.Fatal(err)
		}
		switch obj.Type {
		case "commit":
			commit := ParseCommit(obj)
			// commit edges to parents
			for _, p := range commit.Parents {
				edges = append(edges, Edge{Src: obj.Name, Dest: p})
			}
			// commit edge to tree
			edges = append(edges, Edge{Src: obj.Name, Dest: commit.Tree})
			nodes = append(nodes, map[string]any{"name": obj.Name, "type": obj.Type, "object": objMap})
		case "tree":
			entries := ParseTree(obj)
			// tree to blob edges
			for _, entry := range entries {
				edges = append(edges, Edge{Src: obj.Name, Dest: entry.Hash})
			}
			nodes = append(nodes, map[string]any{"name": obj.Name, "type": obj.Type, "object": objMap, "commit": r.GetTreeCommit(obj.Name, commits)})
		case "blob":
			firstCommitRef := ""
			c, _, err := r.FindFirstInstanceOfBlob(obj.Name, commits)
			if err != nil {
				slog.Error(err.Error())
			} else {
				firstCommitRef = c.Name
			}
			nodes = append(nodes, map[string]any{"name": obj.Name, "type": obj.Type, "object": objMap, "firstCommitRef": firstCommitRef})
		default:
			nodes = append(nodes, map[string]any{"name": obj.Name, "type": obj.Type, "object": objMap})
		}
		return Data{Edges: edges, Nodes: nodes}
	}

	edges := []Edge{}
	nodes := []map[string]any{}

	commits := r.GetCommits(true)
	var inputs []*Input
	for _, obj := range r.Objects {
		inputs = append(inputs, &Input{Commits: commits, Obj: obj})
	}
	for d := range ParallelWork(inputs, getNodesAndEdges, runtime.NumCPU()) {
		edges = append(edges, d.Edges...)
		nodes = append(nodes, d.Nodes...)
	}

	// add refs/branches
	head := r.Head()
	nodes = append(nodes, map[string]any{"name": "HEAD", "type": "ref", "object": head})
	headObj, err := r.GetObject(head.Value)
	if err == nil {
		edges = append(edges, Edge{Src: "HEAD", Dest: headObj.Name})
	} else if r.BranchExist(filepath.Base(head.Value)) {
		edges = append(edges, Edge{Src: "HEAD", Dest: filepath.Base(head.Value)})
	}
	for _, b := range r.Branches() {
		nodes = append(nodes, map[string]any{"name": b.Name, "type": "ref", "object": b})
		edges = append(edges, Edge{Src: b.Name, Dest: b.Commit})
	}

	repoGraph, err := json.MarshalIndent(map[string]any{"nodes": nodes, "edges": edges}, "", tab)
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
	execSql(db, `CREATE tabLE objects (name text primary key, type text, object jsonb);`)
	execSql(db, `CREATE tabLE edges (src text, dest text);`)
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

	insertObjectTotables := func(obj *Object) int {
		_, err = objsStmt.Exec(obj.Name, obj.Type, obj.ToJson())
		if err != nil {
			log.Fatal(err)
		}
		switch obj.Type {
		case "commit":
			commit := ParseCommit(obj)
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
			entries := ParseTree(obj)
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
	for n := range ParallelWork(slices.Collect(maps.Values(r.Objects)), insertObjectTotables, runtime.NumCPU()) {
		bar.Add(n)
	}
}

func (r *Repo) refresh() {
	objects := GetObjects(r.Location)
	r.Objects = objects
}

func (r *Repo) Head() Head {
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

func (r *Repo) CurrBranch() *Branch {
	head := r.Head()
	return NewBranch(r.Location + fmt.Sprintf("/%s/", git) + head.Value)
}

func (r *Repo) CurrCommit() *Commit {
	branch := r.CurrBranch()
	obj, err := r.GetObject(branch.Commit)
	if err != nil {
		log.Fatal(err)
	}
	return ParseCommit(obj)
}

func (r *Repo) Branches() []*Branch {
	var branches []*Branch
	filepath.WalkDir(r.Location+fmt.Sprintf("/%s/refs/heads", git), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			log.Fatal(err)
		}
		if !d.IsDir() {
			branches = append(branches, NewBranch(path))
		}
		return nil
	})
	return branches
}

func (r *Repo) BranchExist(branch string) bool {
	for _, b := range r.Branches() {
		if b.Name == branch {
			return true
		}
	}
	return false
}
