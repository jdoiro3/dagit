package main

import (
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gorilla/websocket"
	_ "github.com/mattn/go-sqlite3"
	"github.com/urfave/cli/v2"
)

const (
	// Time allowed to write the file to the client.
	writeWait = 15 * time.Second
	// Time allowed to read the next pong message from the client.
	pongWait = 60 * time.Second
	// Send pings to client with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10
	// Poll git repo for changes with this period.
	repoPeriod = 10 * time.Second
	// message client sends to get objects even if no changes occurred
	needObjects = "need-objects"
)

var (
	addr      = flag.String("addr", ":8080", "http service address")
	homeTempl = template.Must(template.New("").Parse(homeHTML))
	repo      *Repo
	dir       string
	upgrader  = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     func(r *http.Request) bool { return true },
	}
)

func getNumberOfFiles(p string) int {
	i := 0
	paths, err := os.ReadDir(p)
	if err != nil {
		log.Fatal(err, p)
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
		_, p, err := ws.ReadMessage()
		if err != nil {
			break
		}
		if string(p) == needObjects {
			ws.SetWriteDeadline(time.Now().Add(writeWait))
			if err := ws.WriteMessage(websocket.TextMessage, repo.toJson()); err != nil {
				return
			}
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
			objects = getObjectsIfChange(repo.location, numFiles)

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

	var num int = getNumberOfFiles(repo.location)
	var numFiles *int = &num
	go writer(ws, numFiles)
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

	var v = struct {
		Host        string
		NeedObjects string
	}{
		r.Host,
		needObjects,
	}
	homeTempl.Execute(w, &v)
}

const homeHTML = `<!DOCTYPE html>
<html lang="en">
    <head>
        <title>WebSocket Example</title>
    </head>
    <body>
        <script type="text/javascript">
            (function() {
                var conn = new WebSocket("ws://{{.Host}}/ws");
				conn.onopen = function(evt) {
					console.log("conn open");
					conn.send("{{.NeedObjects}}");
				}
                conn.onclose = function(evt) {
                    console.log('Connection closed');
                }
                conn.onmessage = function(evt) {
                    console.log(JSON.parse(evt.data));
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
