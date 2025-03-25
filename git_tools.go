package main

import (
	"encoding/hex"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ChimeraCoder/gitgo"
)

// gets the object's type (e.g., blob)
func getType(data []byte) (string, int, error) {
	spaceIndex, err := findFirstMatch(SPACE, 0, data)
	if err != nil {
		slog.Warn(err.Error())
		return "", -1, fmt.Errorf("could not get type given byte sequence: % x", data)
	}
	type_ := string(data[0:spaceIndex])
	return strings.TrimSpace(type_), spaceIndex, nil
}

// gets the object's size
func getSize(spaceIndex int, data []byte) (string, int, error) {
	nulIndex, err := findFirstMatch(NUL, spaceIndex+1, data)
	if err != nil {
		slog.Warn(err.Error())
		return "", -1, fmt.Errorf("could not get size given byte sequence: % x", data)
	}
	objSize := string(data[spaceIndex:nulIndex])
	// the second return value is the start of the object's content
	return strings.TrimSpace(objSize), nulIndex + 1, nil
}

func getObjectName(objPath string) string {
	return filepath.Base(filepath.Dir(objPath)) + filepath.Base(objPath)
}

func ChangeExtension(path string, newExt string) string {
	ext := filepath.Ext(path)
	if ext != "" {
		path = strings.TrimSuffix(path, ext)
	}
	if newExt != "" && !strings.HasPrefix(newExt, ".") {
		newExt = "." + newExt
	}
	return path + newExt
}

func getPackedObjects(packPath string) []*Object {
	packFile, err := os.Open(packPath)
	if err != nil {
		log.Fatal(err)
	}
	defer packFile.Close()
	idxFile, err := os.Open(ChangeExtension(packPath, "idx"))
	if err != nil {
		log.Fatal(err)
	}
	defer idxFile.Close()
	packedObjs, err := gitgo.VerifyPack(packFile, idxFile)
	if err != nil {
		log.Fatal(err)
	}
	var objects []*Object
	for _, po := range packedObjs {
		objects = append(objects, &Object{
			Type:     po.Type(),
			Size:     strconv.Itoa(po.Size),
			Location: string(po.Name),
			Name:     string(po.Name),
			Content:  po.PatchedData,
		})
	}
	return objects
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
		} else if filepath.Ext(path) == ".pack" {
			for _, obj := range getPackedObjects(path) {
				objects[obj.Name] = obj
			}
		}
		return nil
	})
	return objects
}

func gitDir(location string) string {
	return location + "/" + GIT
}

func newBranch(f string) Branch {
	name := filepath.Base(f)
	bytes, err := os.ReadFile(f)
	if err != nil {
		log.Fatal(err)
	}
	return Branch{Name: name, Commit: strings.Trim(string(bytes), "\n")}
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
