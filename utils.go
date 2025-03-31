package main

import (
	"bytes"
	"compress/zlib"
	"database/sql"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"sync"
	"time"
)

func ParallelWork[T any, R any](data []T, task func(T) R, workers int) <-chan R {
	results := make(chan R)
	tasks := make(chan T, len(data))
	var wg sync.WaitGroup

	worker := func(id int, tasks <-chan T, results chan<- R) {
		for t := range tasks {
			results <- task(t)
		}
	}

	for i := range workers {
		wg.Add(1)
		go func(id int, tasks <-chan T, results chan<- R) {
			defer wg.Done()
			worker(i, tasks, results)
		}(i, tasks, results)
	}

	for _, d := range data {
		tasks <- d
	}
	close(tasks)

	go func(results chan R) {
		wg.Wait()
		close(results)
	}(results)

	return results
}

// Given a byte find the first byte in a data slice that equals the match_byte, returning the index.
// If no match is found, returns -1 and an error
func findFirstMatch(match byte, start int, data []byte) (int, error) {
	for i, this_byte := range data[start:] {
		if this_byte == match {
			return start + i, nil
		}
	}
	return -1, fmt.Errorf("could not find %x in '% x'", match, data)
}

func getTime(unixTime string) time.Time {
	i, err := strconv.ParseInt(unixTime, 10, 64)
	if err != nil {
		log.Fatal(err)
	}
	return time.Unix(i, 0)
}

func execSql(db *sql.DB, query string) sql.Result {
	result, err := db.Exec(query)
	if err != nil {
		log.Fatal(err)
	}
	return result
}

func getBytes(path string, compressed bool) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatal(path)
	}
	if compressed {
		// zlib expects an io.Reader object
		reader, err := zlib.NewReader(bytes.NewReader(data))
		if err != nil {
			log.Fatal(err)
		}
		data, err = io.ReadAll(reader)
		if err != nil {
			log.Fatal(err)
		}
	}
	return data
}

func removeFromSlice[T any](s []T, indexes []int) []T {
	for i := range indexes {
		s[i] = s[len(s)-1]
		s = s[:len(s)-1]
	}
	return s
}
