package main

import (
	"bytes"
	"compress/zlib"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
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
)

// Given a byte find the first byte in a data slice that equals the match_byte, returning the index.
// If no match is found, returns -1
func findFirstMatch(match_byte byte, start_index int, data *[]byte) int {
	for i, this_byte := range (*data)[start_index:] {
		if this_byte == match_byte {
			return start_index + i
		}
	}
	return -1
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

func getType(data *[]byte) (string, int) {
	first_space_index := findFirstMatch(SPACE, 0, data)
	type_ := string((*data)[0:first_space_index])
	return strings.TrimSpace(type_), first_space_index
}

// gets the object's size
func getSize(first_space_index int, data *[]byte) (string, int) {
	first_nul_index := findFirstMatch(NUL, first_space_index+1, data)
	obj_size := string((*data)[first_space_index:first_nul_index])
	// second return value is the start of the object's content
	return strings.TrimSpace(obj_size), first_nul_index + 1
}

func getObjectName(object_path string) string {
	object_dir := filepath.Base(filepath.Dir(object_path))
	name := object_dir + filepath.Base(object_path)
	return name
}

func newObject(object_path string) *Object {
	zlib_bytes, err := os.ReadFile(object_path)
	if err != nil {
		log.Fatal(err)
	}
	// zlib expects an io.Reader object
	reader, err := zlib.NewReader(bytes.NewReader(zlib_bytes))
	if err != nil {
		log.Fatal(err)
	}
	bytes, err := io.ReadAll(reader)
	if err != nil {
		log.Fatal(err)
	}
	data_ptr := &bytes
	type_, first_space_index := getType(data_ptr)
	size, content_start_index := getSize(first_space_index, data_ptr)
	return &Object{type_, size, object_path, getObjectName(object_path), bytes[content_start_index:]}
}

func (obj *Object) toJson() []byte {
	switch obj.Type {
	case "tree":
		json_tree, err := json.Marshal(map[string][]TreeEntry{"entries": *parseTree(obj)})
		if err != nil {
			log.Fatal(err)
		}
		return json_tree
	case "commit":
		json_commit, err := json.Marshal(parseCommit(obj))
		if err != nil {
			log.Fatal(err)
		}
		return json_commit
	case "blob":
		json_blob, err := json.Marshal(parseBlob(obj))
		if err != nil {
			log.Fatal(err)
		}
		return json_blob
	default:
		return make([]byte, 0)
	}
}

func getObjects(objects_dir string) map[string]*Object {
	objects := make(map[string]*Object)
	filepath.WalkDir(objects_dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			log.Fatal(err)
		}
		is_hex, err := regexp.MatchString("^[a-fA-F0-9]+$", filepath.Base(path))
		if err != nil {
			log.Fatal(err)
		}
		if !d.IsDir() && is_hex {
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
		log.Printf("%s != %s", r.checksum, dirHash)
		r.checksum = dirHash
		return true
	}
	return false
}

func (r *Repo) getObject(name string) *Object {
	return r.objects[name]
}

func (r *Repo) toJson() []byte {
	edges := []Edge{}
	nodes := []map[string]any{}
	// add objects
	for _, obj := range r.objects {
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
	}
	// add refs/branches
	head := r.head()
	nodes = append(nodes, map[string]any{"name": "HEAD", "type": "ref", "object": head})
	edges = append(edges, Edge{Src: "HEAD", Dest: filepath.Base(head.Value)})
	for _, b := range r.branches() {
		nodes = append(nodes, map[string]any{"name": b.Name, "type": "ref", "object": b})
		edges = append(edges, Edge{Src: b.Name, Dest: b.Commit})
	}

	repo_json, err := json.Marshal(map[string]any{"nodes": nodes, "edges": edges})
	if err != nil {
		log.Fatal(err)
	}
	return repo_json
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
	return parseCommit(r.getObject(branch.Commit))
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
