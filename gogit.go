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
	"strings"

	_ "github.com/mattn/go-sqlite3"
	"github.com/schollz/progressbar/v3"
	"github.com/urfave/cli/v2"
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

const (
	SPACE byte = 32
	NUL   byte = 0
)

type Object struct {
	type_    string `json:"type"`
	size     string `json:"size"`
	location string `json:"location"`
	name     string `json:"name"`
	content  []byte `json:"content"`
}

type TreeEntry struct {
	mode string
	name string
	hash string
}

type Commit struct {
	tree    string
	parents []string
}

type Repo struct {
	location string
	objects  map[string]*Object
}

func getType(data *[]byte) (string, int) {
	first_space_index := findFirstMatch(SPACE, 0, data)
	type_ := string((*data)[0:first_space_index])
	return strings.TrimSpace(type_), first_space_index
}

// second return value is the start of the object's content
func getSize(first_space_index int, data *[]byte) (string, int) {
	first_nul_index := findFirstMatch(NUL, first_space_index+1, data)
	obj_size := string((*data)[first_space_index:first_nul_index])
	return strings.TrimSpace(obj_size), first_nul_index + 1
}

func newObject(object_path string) *Object {
	zlib_bytes, err := os.ReadFile(object_path)
	if err != nil {
		panic(err)
	}
	// zlib expects an io.Reader object
	reader, err := zlib.NewReader(bytes.NewReader(zlib_bytes))
	if err != nil {
		panic(err)
	}
	bytes, err := io.ReadAll(reader)
	if err != nil {
		panic(err)
	}
	data_ptr := &bytes
	type_, first_space_index := getType(data_ptr)
	size, content_start_index := getSize(first_space_index, data_ptr)
	object_dir := filepath.Base(filepath.Dir(object_path))
	return &Object{type_, size, object_path, object_dir + filepath.Base(object_path), bytes[content_start_index:]}
}

func (obj *Object) toJson() string {
	switch obj.type_ {
	case "tree":
		entries := parseTree(obj)
		output := "[\n"
		for i, entry := range entries {
			if i == len(entries)-1 {
				output += entry.toJson() + "\n"
			} else {
				output += entry.toJson() + ",\n"
			}
		}
		return output + "]\n"
	case "commit":
		commit := parseCommit(obj)
		parents, _ := json.Marshal(commit.parents)
		return fmt.Sprintf(`{"parents": %s, "tree": "%s"}`, string(parents), commit.tree)
	case "blob":
		return "\"" + strings.Replace(string(obj.content), `"`, `\"`, -1) + "\""
	default:
		return fmt.Sprintf("I'm a %s\n", obj.type_)
	}
}

func (e *TreeEntry) toJson() string {
	return fmt.Sprintf(`{"mode": "%s", "name": "%s", "hash": "%s"}`, e.mode, e.name, e.hash)
}

func getObjectName(object_path string) string {
	object_dir := filepath.Base(filepath.Dir(object_path))
	name := object_dir + filepath.Base(object_path)
	return name
}

func getObjects(objects_dir string) map[string]*Object {
	var objects map[string]*Object
	objects = make(map[string]*Object)
	filepath.WalkDir(objects_dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		is_hex, err := regexp.MatchString("^[a-fA-F0-9]+$", filepath.Base(path))
		if !d.IsDir() && is_hex {
			obj := newObject(path)
			objects[obj.name] = obj
		}
		return nil
	})
	return objects
}

func newRepo(location string) *Repo {
	objects := getObjects(location + "/.git/objects")
	return &Repo{location, objects}
}

func (r *Repo) getObject(name string) *Object {
	return r.objects[name]
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
		_, err = objs_stmt.Exec(name, obj.type_, obj.toJson())
		if err != nil {
			log.Fatal(err)
		}
		switch obj.type_ {
		case "commit":
			commit := parseCommit(obj)
			// commit edges to parents
			for _, p := range commit.parents {
				_, err = edges_stmt.Exec(obj.name, p)
				if err != nil {
					log.Fatal(err)
				}
			}
			// commit edge to tree
			_, err = edges_stmt.Exec(obj.name, commit.tree)
			if err != nil {
				log.Fatal(err)
			}
		case "tree":
			entries := parseTree(obj)
			// tree to blob edges
			for _, entry := range entries {
				_, err = edges_stmt.Exec(obj.name, entry.hash)
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

func (r *Repo) head() string {
	bytes, err := os.ReadFile(r.location + "/.git/HEAD")
	if err != nil {
		panic(err)
	}
	return strings.TrimSpace(strings.Split(string(bytes), ":")[1])
}

func (r *Repo) branch() string {
	head := r.head()
	return filepath.Base(head)
}

func (r *Repo) current_commit() *Object {
	bytes, err := os.ReadFile(r.location + "/.git/" + r.head())
	if err != nil {
		panic(err)
	}
	return r.getObject(strings.TrimSpace(string(bytes)))
}

func parseTree(obj *Object) []TreeEntry {
	var entries []TreeEntry
	entry_item, start, stop := 1, 0, 6
	mode, name, hash := "", "", ""
	for stop <= len(obj.content) {
		switch entry_item {
		// get the mode
		case 1:
			mode = strings.TrimSpace(string(obj.content[start:stop]))
			entry_item += 1
			start = stop
		// get the name (file or dir)
		case 2:
			i := start
			for obj.content[i] != NUL && i < len(obj.content)-1 {
				i += 1
			}
			name = strings.TrimSpace(string(obj.content[start:i]))
			entry_item += 1
			start = i + 1
			stop = start + 20
		// get the hash (object name)
		case 3:
			hash = strings.TrimSpace(hex.EncodeToString(obj.content[start:stop]))
			entry_item = 1
			start = stop
			stop = start + 6
			entries = append(entries, TreeEntry{mode, name, hash})
		}
	}
	return entries
}

func parseCommit(obj *Object) Commit {
	tree_hash := string(obj.content[5:45])
	rest_of_content := strings.Split(string(obj.content[46:]), "\n")
	var parents []string
	for _, line := range rest_of_content {
		if line[:6] == "parent" {
			parents = append(parents, line[7:47])
		} else {
			break
		}
	}
	return Commit{tree_hash, parents}
}

func main() {
	var repo_path string
	var db_path string

	app := &cli.App{
		UseShortOptionHandling: true,
		Commands: []*cli.Command{
			{
				Name:  "to-sqlite",
				Usage: "dfsdf",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:        "repo-path",
						Value:       ".",
						Aliases:     []string{"r"},
						Destination: &repo_path,
						Usage:       "Pass multiple greetings",
					},
					&cli.StringFlag{
						Name:        "db-path",
						Value:       "git.sqlite",
						Aliases:     []string{"d"},
						Destination: &db_path,
						Usage:       "Pass multiple greetings",
					},
				},
				Action: func(cCtx *cli.Context) error {
					repo := newRepo(repo_path)
					repo.toSQLite(db_path)
					return nil
				},
			},
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
