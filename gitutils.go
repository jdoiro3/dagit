package main

import (
	"bytes"
	"compress/zlib"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gosimple/hashdir"
	"github.com/schollz/progressbar/v3"
)

const (
	SPACE byte   = 32
	NUL   byte   = 0
	GIT   string = ".git"
	TAB   string = "    "
)

// Consumes a channel and adds values to a slice, returning the slice.
func toSlice[T interface{}](c chan T) []T {
	s := make([]T, 0)
	for i := range c {
		s = append(s, i)
	}
	return s
}

// Given a byte find the first byte in a data slice that equals the match_byte, returning the index.
// If no match is found, returns -1 and an error
func findFirstMatch(match byte, start int, data *[]byte) (int, error) {
	for i, this_byte := range (*data)[start:] {
		if this_byte == match {
			return start + i, nil
		}
	}
	return -1, errors.New(fmt.Sprintf("Could not find %x in '% x'", match, data))
}

func getTime(unixTime string) time.Time {
	i, err := strconv.ParseInt(unixTime, 10, 64)
	if err != nil {
		log.Fatal(err)
	}
	return time.Unix(i, 0)
}

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
	location string
	objects  map[string]*Object
	checksum string
}

// gets the object's type (e.g., blob)
func getType(data *[]byte) (string, int, error) {
	spaceIndex, err := findFirstMatch(SPACE, 0, data)
	if err != nil {
		slog.Warn(err.Error())
		return "", -1, fmt.Errorf("could not get type given byte sequence: % x", data)
	}
	type_ := string((*data)[0:spaceIndex])
	return strings.TrimSpace(type_), spaceIndex, nil
}

// gets the object's size
func getSize(spaceIndex int, data *[]byte) (string, int, error) {
	nulIndex, err := findFirstMatch(NUL, spaceIndex+1, data)
	if err != nil {
		slog.Warn(err.Error())
		return "", -1, fmt.Errorf("could not get size given byte sequence: % x", data)
	}
	objSize := string((*data)[spaceIndex:nulIndex])
	// the second return value is the start of the object's content
	return strings.TrimSpace(objSize), nulIndex + 1, nil
}

func getObjectName(objPath string) string {
	return filepath.Base(filepath.Dir(objPath)) + filepath.Base(objPath)
}

func newObject(objectPath string) *Object {
	zlibBytes, err := os.ReadFile(objectPath)
	if err != nil {
		log.Fatal(objectPath)
	}
	// zlib expects an io.Reader object
	reader, err := zlib.NewReader(bytes.NewReader(zlibBytes))
	if err != nil {
		log.Fatal(err)
	}
	bytes, err := io.ReadAll(reader)
	if err != nil {
		log.Fatal(err)
	}
	data := &bytes
	objType, spaceIndex, err := getType(data)
	if err != nil {
		slog.Warn(err.Error())
	}
	size, contentStart, err := getSize(spaceIndex, data)
	if err != nil {
		slog.Warn(err.Error())
	}
	return &Object{objType, size, objectPath, getObjectName(objectPath), bytes[contentStart:]}
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
		return make([]byte, 0)
	}
}

func getObjects(objDir string) map[string]*Object {
	objects := make(map[string]*Object)
	filepath.WalkDir(objDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			log.Fatal(err)
		}
		isHex, err := regexp.MatchString("^[a-fA-F0-9]+$", filepath.Base(path))
		if err != nil {
			log.Fatal(err)
		}
		if !d.IsDir() && isHex {
			obj := newObject(path)
			objects[obj.Name] = obj
		}
		return nil
	})
	return objects
}

func gitDir(location string) string {
	return location + "/" + GIT
}

func newRepo(location string) *Repo {
	objects := getObjects(gitDir(location) + "/objects")
	dirHash, err := hashdir.Make(gitDir(location), "md5")
	if err != nil {
		log.Fatal(err)
	}
	return &Repo{
		location: location,
		objects:  objects,
		checksum: dirHash,
	}
}

func (r *Repo) changed() bool {
	dirHash, err := hashdir.Make(gitDir(r.location), "md5")
	if err != nil {
		log.Fatal(err)
	}
	if r.checksum != dirHash {
		r.checksum = dirHash
		return true
	}
	return false
}

func (r *Repo) getObject(name string) (*Object, error) {
	obj, ok := r.objects[name]
	if ok {
		return obj, nil
	}
	return nil, fmt.Errorf("Object, %v, doesn't seem to exist in the repo", name)
}

func (r *Repo) toJsonGraph() []byte {
	edgesChan := make(chan Edge)
	nodesChan := make(chan map[string]any)
	// add objects
	for _, obj := range r.objects {
		go func(obj *Object, edgesChan chan Edge, nodesChan chan map[string]any) {
			var objMap map[string]json.RawMessage
			err := json.Unmarshal(obj.toJson(), &objMap)
			if err != nil {
				log.Fatal(err)
			}
			nodesChan <- map[string]any{"name": obj.Name, "type": obj.Type, "object": objMap}
			switch obj.Type {
			case "commit":
				commit := parseCommit(obj)
				// commit edges to parents
				for _, p := range commit.Parents {
					edgesChan <- Edge{Src: obj.Name, Dest: p}
				}
				// commit edge to tree
				edgesChan <- Edge{Src: obj.Name, Dest: commit.Tree}
			case "tree":
				entries := *parseTree(obj)
				// tree to blob edges
				for _, entry := range entries {
					edgesChan <- Edge{Src: obj.Name, Dest: entry.Hash}
				}
			}
		}(obj, edgesChan, nodesChan)
	}
	// add refs/branches
	head := r.head()
	nodesChan <- map[string]any{"name": "HEAD", "type": "ref", "object": head}
	edgesChan <- Edge{Src: "HEAD", Dest: filepath.Base(head.Value)}
	for _, b := range r.branches() {
		nodesChan <- map[string]any{"name": b.Name, "type": "ref", "object": b}
		edgesChan <- Edge{Src: b.Name, Dest: b.Commit}
	}
	repoGraph, err := json.MarshalIndent(map[string]any{"nodes": toSlice(nodesChan), "edges": toSlice(edgesChan)}, "", TAB)
	if err != nil {
		log.Fatal(err)
	}
	return repoGraph
}

func exec(db *sql.DB, query string) sql.Result {
	result, err := db.Exec(query)
	if err != nil {
		log.Fatal(err)
	}
	return result
}

func (r *Repo) toSQLite(path string) {
	os.Remove(path)

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	exec(db, `create table objects (name text primary key, type text, object jsonb);`)
	exec(db, `create table edges (src text, dest text);`)
	objs_stmt, err := db.Prepare("insert into objects(name, type, object) values(?, ?, ?)")
	if err != nil {
		log.Fatal(err)
	}
	edges_stmt, err := db.Prepare("insert into edges(src, dest) values(?, ?)")
	if err != nil {
		log.Fatal(err)
	}
	defer objs_stmt.Close()
	defer edges_stmt.Close()

	fmt.Println("[info] generating Git SQLite database...")
	bar := progressbar.Default(int64(len(r.objects)))
	for name, obj := range r.objects {
		_, err = objs_stmt.Exec(name, obj.Type, obj.toJson())
		if err != nil {
			log.Fatal(err)
		}
		switch obj.Type {
		case "commit":
			commit := parseCommit(obj)
			// commit edges to parents
			for _, p := range commit.Parents {
				_, err = edges_stmt.Exec(obj.Name, p)
				if err != nil {
					log.Fatal(err)
				}
			}
			// commit edge to tree
			_, err = edges_stmt.Exec(obj.Name, commit.Tree)
			if err != nil {
				log.Fatal(err)
			}
		case "tree":
			entries := *parseTree(obj)
			// tree to blob edges
			for _, entry := range entries {
				_, err = edges_stmt.Exec(obj.Name, entry.Hash)
				if err != nil {
					log.Fatal(err)
				}
			}
		}
		bar.Add(1)
	}
}

func (r *Repo) refresh() {
	objects := getObjects(r.location)
	r.objects = objects
}

func (r *Repo) head() Head {
	bytes, err := os.ReadFile(gitDir(r.location) + "/HEAD")
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

func newBranch(f string) Branch {
	name := filepath.Base(f)
	bytes, err := os.ReadFile(f)
	if err != nil {
		log.Fatal(err)
	}
	return Branch{Name: name, Commit: strings.Trim(string(bytes), "\n")}
}

func (r *Repo) currBranch() Branch {
	head := r.head()
	return newBranch(r.location + fmt.Sprintf("/%s/", GIT) + head.Value)
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
	filepath.WalkDir(r.location+fmt.Sprintf("/%s/refs/heads", GIT), func(path string, d fs.DirEntry, err error) error {
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

func parseBlob(obj *Object) Blob {
	size, err := strconv.Atoi(obj.Size)
	if err != nil {
		log.Fatal(err)
	}
	return Blob{Content: string(obj.Content), Size: size}
}

func parseTree(obj *Object) *[]TreeEntry {
	var entries []TreeEntry
	content_len := len(obj.Content)
	entry_item, start, stop := 1, 0, 6 // TODO: don't use magic numbers. Define constants.
	mode, name, hash := "", "", ""
	for stop <= content_len {
		switch entry_item {
		// get the mode
		case 1:
			mode = strings.TrimSpace(string(obj.Content[start:stop]))
			entry_item += 1
			start = stop
		// get the name (file or dir)
		case 2:
			i := start
			for obj.Content[i] != NUL && i < content_len-1 {
				i += 1
			}
			name = strings.TrimSpace(string(obj.Content[start:i]))
			entry_item += 1
			start = i + 1
			stop = start + 20 // TODO: don't use magic numbers. Define constants.
		// get the hash (object name)
		case 3:
			hash = strings.TrimSpace(hex.EncodeToString(obj.Content[start:stop]))
			entry_item = 1
			start = stop
			stop = start + 6 // TODO: don't use magic numbers. Define constants.
			entries = append(entries, TreeEntry{mode, name, hash})
		}
	}
	return &entries
}

func parseCommit(obj *Object) Commit {
	tree_hash := string(obj.Content[5:45]) // TODO: don't use magic numbers. Define constants.
	content := string(obj.Content[46:])
	rest_of_content := strings.Split(content, "\n")
	// The commit message looks to be separated by two newlines and ends with a newline
	msg := strings.Trim(strings.Split(content, "\n\n")[1], "\n")

	var parents []string
	var author User
	var committer User
	var commitTime time.Time
	var authorTime time.Time

	for _, line := range rest_of_content {
		if len(line) < 9 {
			continue
		}
		if line[:6] == "parent" {
			parents = append(parents, line[7:47]) // TODO: don't use magic numbers. Define constants.
		} else if line[:6] == "author" {
			nameEnd := strings.Index(line, "<")
			name := line[7:nameEnd]
			var authorLine []string = strings.Split(line[nameEnd:], " ")
			authorTime = getTime(authorLine[1])
			author = User{Name: name, Email: authorLine[0]}
		} else if line[:9] == "committer" {
			nameEnd := strings.Index(line, "<")
			name := line[10:nameEnd]
			var commiterLine []string = strings.Split(line[nameEnd:], " ")
			commitTime = getTime(commiterLine[1])
			committer = User{Name: name, Email: commiterLine[0]}
		}
	}
	return Commit{tree_hash, parents, author, committer, msg, commitTime, authorTime}
}
