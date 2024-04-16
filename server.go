package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// Time allowed to write git objects to client.
	writeWait = 10 * time.Second
	// Time allowed to read the next pong message from the client.
	pongWait = 10 * time.Second
	// Send pings to client with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10
	// Poll git repo for changes with this period.
	repoPeriod = 5 * time.Second
	// message client sends to get objects even if no changes occurred
	needObjects = "need-objects"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

func getNumberOfFiles(p string) int {
	i := 0
	paths, err := os.ReadDir(p)
	if err != nil {
		log.Println(p)
		log.Fatal(err)
	}
	for _, pe := range paths {
		if pe.IsDir() {
			i += getNumberOfFiles(filepath.Join(p, pe.Name()))
		} else {
			i++
		}
	}
	return i
}

func getObjectsIfChange(objsDir string, numFiles *int) []byte {
	newNumFiles := getNumberOfFiles(objsDir)
	if newNumFiles != *numFiles {
		log.Printf("numFiles = %d, newNumFiles = %d\n", *numFiles, newNumFiles)
		*numFiles = newNumFiles
		repo.refresh()
		return repo.toJson()
	}
	return nil
}

func reader(ws *websocket.Conn) {
	defer ws.Close()
	ws.SetReadLimit(512)
	ws.SetReadDeadline(time.Now().Add(pongWait))
	ws.SetPongHandler(func(string) error { ws.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		_, msg, err := ws.ReadMessage()
		if err != nil {
			break
		}
		if string(msg) == needObjects {
			log.Printf("objects from %s requested from client ...\n", repo.location)
			objects := repo.toJson()
			ws.SetWriteDeadline(time.Now().Add(writeWait))
			if err := ws.WriteMessage(websocket.TextMessage, objects); err != nil {
				return
			}
			log.Println("objects sent to client.")
		}
	}
}

func writer(ws *websocket.Conn, numFiles *int) {
	pingTicker := time.NewTicker(pingPeriod)
	repoTicker := time.NewTicker(repoPeriod)

	defer func() {
		pingTicker.Stop()
		repoTicker.Stop()
		ws.Close()
	}()

	for {
		select {
		case <-repoTicker.C:

			var objects []byte = nil
			objects = getObjectsIfChange(repo.location+fmt.Sprintf("/%s", GIT_DIR), numFiles)

			if objects != nil {
				ws.SetWriteDeadline(time.Now().Add(writeWait))
				if err := ws.WriteMessage(websocket.TextMessage, objects); err != nil {
					return
				}
			}
		case <-pingTicker.C:
			ws.SetWriteDeadline(time.Now().Add(writeWait))
			if err := ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func serveWs(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		if _, ok := err.(websocket.HandshakeError); !ok {
			log.Println(err)
		}
		return
	}

	var num int = getNumberOfFiles(repo.location + fmt.Sprintf("/%s", GIT_DIR))
	var numFiles *int = &num
	go writer(ws, numFiles)
	reader(ws)
}
