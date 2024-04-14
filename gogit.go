package main

import (
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
	_ "github.com/mattn/go-sqlite3"
	"github.com/urfave/cli/v2"
)

const (
	// Time allowed to write the file to the client.
	writeWait = 10 * time.Second
	// Time allowed to read the next pong message from the client.
	pongWait = 60 * time.Second
	// Send pings to client with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10
	// Poll file for changes with this period.
	filePeriod = 10 * time.Second
)

var (
	addr      = flag.String("addr", ":8080", "http service address")
	homeTempl = template.Must(template.New("").Parse(homeHTML))
	repo      *Repo
	dir       string
	upgrader  = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}
)

func getDirs(root string) ([]byte, error) {
	var files []byte
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if !info.IsDir() {
			files = append(append(files, []byte("\n")...), path...)
		}
		return nil
	})
	return files, err
}

func getObjectsIfModified(lastMod time.Time) ([]byte, time.Time, error) {
	di, err := os.Stat(dir)
	if err != nil {
		return nil, lastMod, err
	}
	if !di.ModTime().After(lastMod) {
		return nil, lastMod, nil
	}

	repo.refresh()
	return []byte(repo.toJson()), di.ModTime(), nil
}

func reader(ws *websocket.Conn) {
	defer ws.Close()
	ws.SetReadLimit(512)
	ws.SetReadDeadline(time.Now().Add(pongWait))
	ws.SetPongHandler(func(string) error { ws.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		_, _, err := ws.ReadMessage()
		if err != nil {
			break
		}
	}
}

func writer(ws *websocket.Conn, lastMod time.Time) {
	lastError := ""
	pingTicker := time.NewTicker(pingPeriod)
	fileTicker := time.NewTicker(filePeriod)
	defer func() {
		pingTicker.Stop()
		fileTicker.Stop()
		ws.Close()
	}()
	for {
		select {
		case <-fileTicker.C:
			var objects []byte
			var err error

			objects, lastMod, err = getObjectsIfModified(lastMod)

			if err != nil {
				if s := err.Error(); s != lastError {
					lastError = s
					objects = []byte(lastError)
				}
			} else {
				lastError = ""
			}

			if objects != nil {
				ws.SetWriteDeadline(time.Now().Add(writeWait))
				if err := ws.WriteMessage(websocket.TextMessage, objects); err != nil {
					return
				}
			}
		case <-pingTicker.C:
			ws.SetWriteDeadline(time.Now().Add(writeWait))
			if err := ws.WriteMessage(websocket.PingMessage, []byte{}); err != nil {
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

	var lastMod time.Time
	if n, err := strconv.ParseInt(r.FormValue("lastMod"), 16, 64); err == nil {
		lastMod = time.Unix(0, n)
	}

	go writer(ws, lastMod)
	reader(ws)
}

func serveHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	objects, lastMod, err := getObjectsIfModified(time.Time{})
	if err != nil {
		objects = []byte(err.Error())
		lastMod = time.Unix(0, 0)
	}
	var v = struct {
		Host    string
		Data    string
		LastMod string
	}{
		r.Host,
		string(objects),
		strconv.FormatInt(lastMod.UnixNano(), 16),
	}
	homeTempl.Execute(w, &v)
}

const homeHTML = `<!DOCTYPE html>
<html lang="en">
    <head>
        <title>WebSocket Example</title>
    </head>
    <body>
        <pre id="fileData">{{.Data}}</pre>
        <script type="text/javascript">
            (function() {
                var data = document.getElementById("fileData");
                var conn = new WebSocket("ws://{{.Host}}/ws?lastMod={{.LastMod}}");
                conn.onclose = function(evt) {
                    data.textContent = 'Connection closed';
                }
                conn.onmessage = function(evt) {
                    console.log('file updated');
                    data.textContent = evt.data;
                }
            })();
        </script>
    </body>
</html>
`

func main() {

	app := &cli.App{
		UseShortOptionHandling: true,
		Name:                   "gogit",
		Version:                "v1.0.0",
		Compiled:               time.Now(),
		Authors: []*cli.Author{
			&cli.Author{
				Name:  "Joseph Doiron",
				Email: "",
			},
		},
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "repo-path",
				Value:   ".",
				Aliases: []string{"r"},
				Usage:   "",
			},
		},
		Commands: []*cli.Command{
			{
				Name:  "to-sqlite",
				Usage: "Generates a SQLite database representing the Git repo.",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "db-path",
						Value:   "git.sqlite",
						Aliases: []string{"d"},
						Usage:   "The path to the database to output.",
					},
				},
				Action: func(cCtx *cli.Context) error {
					repo := newRepo(cCtx.String("repo-path"))
					repo.toSQLite(cCtx.String("db-path"))
					return nil
				},
			},
			{
				Name:  "serve",
				Usage: "todo",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "repo-path",
						Value:   ".",
						Aliases: []string{"p"},
						Usage:   "todo",
					},
				},
				Action: func(cCtx *cli.Context) error {
					dir = cCtx.String("repo-path")
					repo = newRepo(dir)
					fmt.Printf("Watching %s\n", dir)
					http.HandleFunc("/", serveHome)
					http.HandleFunc("/ws", serveWs)
					server := &http.Server{
						Addr:              *addr,
						ReadHeaderTimeout: 3 * time.Second,
					}
					if err := server.ListenAndServe(); err != nil {
						log.Fatal(err)
					}
					return nil
				},
			},
			{
				Name:  "show",
				Usage: "Shows the content of a Git object.",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "object",
						Aliases: []string{"o"},
						Usage:   "Pass multiple greetings",
					},
					&cli.BoolFlag{Name: "type", Aliases: []string{"t"}},
				},
				Action: func(cCtx *cli.Context) error {
					repo := newRepo(cCtx.String("repo-path"))
					if cCtx.String("object") == "" {
						fmt.Println(string(repo.toJson()))
					} else {
						obj := repo.getObject(cCtx.String("object"))
						if cCtx.Bool("type") {
							fmt.Println(obj.Type_)
						} else {
							fmt.Println(string(obj.toJson()[:]))
						}
					}
					return nil
				},
			},
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
