package main

import (
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"
)

const TAB string = "    "

func parallelWork[T any, R any](data []T, worker func(T) R) <-chan R {
	results := make(chan R)
	var wg sync.WaitGroup
	for _, i := range data {
		wg.Add(1)
		go func(i T) {
			defer wg.Done()
			results <- worker(i)
		}(i)
	}
	go func(wg *sync.WaitGroup, results chan R) {
		wg.Wait()
		close(results)
	}(&wg, results)
	return results
}

// Given a byte find the first byte in a data slice that equals the match_byte, returning the index.
// If no match is found, returns -1 and an error
func findFirstMatch(match byte, start int, data *[]byte) (int, error) {
	for i, this_byte := range (*data)[start:] {
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
