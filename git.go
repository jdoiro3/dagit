package main

import (
	"bytes"
	"compress/zlib"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"io/ioutil"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	SPACE byte = 32
	NUL   byte = 0
)

type Object struct {
	obj_type string
	size     string
	location string
	name     string
	content  []byte
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
	obj_type := string((*data)[0:first_space_index])
	return strings.TrimSpace(obj_type), first_space_index
}

// second return value is the start of the object's content
func getSize(first_space_index int, data *[]byte) (string, int) {
	first_nul_index := findFirstMatch(NUL, first_space_index+1, data)
	obj_size := string((*data)[first_space_index:first_nul_index])
	return strings.TrimSpace(obj_size), first_nul_index + 1
}

func newObject(object_path string) *Object {
	zlib_bytes, err := ioutil.ReadFile(object_path)
	if err != nil {
		panic(err)
	}
	// zlib expects an io.Reader object
	reader, err := zlib.NewReader(bytes.NewReader(zlib_bytes))
	if err != nil {
		panic(err)
	}
	bytes, err := ioutil.ReadAll(reader)
	if err != nil {
		panic(err)
	}
	data_ptr := &bytes
	obj_type, first_space_index := getType(data_ptr)
	size, content_start_index := getSize(first_space_index, data_ptr)
	object_dir := filepath.Base(filepath.Dir(object_path))
	return &Object{obj_type, size, object_path, object_dir + filepath.Base(object_path), bytes[content_start_index:]}
}

func (obj *Object) toJson() string {
	switch obj.obj_type {
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
		return fmt.Sprintf("{ \"parents\": %s, \"tree\": \"%s\"}", string(parents), commit.tree)
	case "blob":
		return "\"" + string(obj.content) + "\""
	default:
		return fmt.Sprintf("I'm a %s\n", obj.obj_type)
	}
}

func (e *TreeEntry) toJson() string {
	return fmt.Sprintf("{\"mode\": \"%s\", \"name\": \"%s\", \"hash\": \"%s\"}", e.mode, e.name, e.hash)
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

func (r *Repo) refresh() {
	objects := getObjects(r.location)
	r.objects = objects
}

func (r *Repo) head() string {
	bytes, err := ioutil.ReadFile(r.location + "/.git/HEAD")
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
	bytes, err := ioutil.ReadFile(r.location + "/.git/" + r.head())
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
	repo := newRepo("/Users/joebob/Desktop/mkdocs-multirepo-plugin")
	obj := repo.getObject("03b07c783a602e71e392006106e9c482e7f5bffd")
	fmt.Println(obj.toJson())
	//repo.refresh()
	fmt.Println(repo.head())
	fmt.Println(repo.branch())
	obj = repo.current_commit()
	fmt.Println(obj.toJson())
}
