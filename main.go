package main

import (
	"context"
	"fmt"
	"mooz/fs"
	"mooz/ui"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/websocket/v2"
)

const (
	ping        = "ping"
	pong        = "pong"
	initialized = "initialized"
	joined      = "joined"
	left        = "left"
)

type Message struct {
	To   string      `json:"to,omitempty"`
	From string      `json:"from,omitempty"`
	Type string      `json:"type"`
	Data interface{} `json:"data,omitempty"`
}

type Client struct {
	id   string
	conn *websocket.Conn
	out  chan Message
}

func main() {
	app := fiber.New(fiber.Config{
		ReadTimeout: 10 * time.Second,
	})

	app.Use(recover.New(), logger.New(), cors.New())

	var (
		ctx, cancel = context.WithCancel(context.Background())
		cs          = map[string]Client{}
		csMx        sync.RWMutex
	)

	broadcast := func(m Message) {
		csMx.RLock()
		for id, c := range cs {
			if id == m.From {
				continue
			}
			c.out <- m
		}
		csMx.RUnlock()
	}

	send := func(m Message) {
		csMx.RLock()
		c, exists := cs[m.To]
		if exists {
			c.out <- m
		}
		csMx.RUnlock()
	}

	app.Get("/ws", websocket.New(func(conn *websocket.Conn) {

		c := Client{
			id:   fmt.Sprintf("%p", conn),
			conn: conn,
			out:  make(chan Message),
		}

		csMx.Lock()
		cs[c.id] = c
		csMx.Unlock()

		var (
			cCtx, cCancel = context.WithCancel(context.Background())
			wg            sync.WaitGroup
			startWg       sync.WaitGroup

			pings   = map[int64]struct{}{}
			pingsMx sync.Mutex
		)

		defer cCancel()

		wg.Add(1)
		startWg.Add(1)
		go func() {
			defer wg.Done()
			startWg.Done()

			var m Message

			for {
				select {
				case <-cCtx.Done():
					return
				case m = <-c.out:
				}

				err := c.conn.WriteJSON(m)
				if err != nil {
					cCancel()
					return
				}
			}
		}()

		wg.Add(1)
		startWg.Add(1)
		go func() {
			defer wg.Done()
			startWg.Done()

			t := time.NewTicker(3 * time.Second)
			defer t.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-cCtx.Done():
					return
				case <-t.C:
				}

				p := time.Now().Unix()

				pingsMx.Lock()
				if len(pings) > 5 {
					cCancel()
					return
				}
				pings[p] = struct{}{}
				pingsMx.Unlock()

				send(Message{
					To:   c.id,
					Type: ping,
					Data: p,
				})
			}
		}()

		wg.Add(1)
		startWg.Add(1)
		go func() {
			defer wg.Done()
			startWg.Done()

			var m Message

			for {
				select {
				case <-ctx.Done():
					return
				case <-cCtx.Done():
					return
				default:
				}

				err := c.conn.ReadJSON(&m)
				if err != nil {
					cCancel()
					return
				}

				switch m.Type {

				case pong:
					rp, ok := m.Data.(float64)
					if !ok {
						cCancel()
						return
					}

					pingsMx.Lock()
					for p := range pings {
						if p >= int64(rp) {
							delete(pings, p)
						}
					}
					pingsMx.Unlock()

				default:
					m.From = c.id
					send(m)
				}
			}
		}()

		startWg.Wait()

		broadcast(Message{
			Type: initialized,
		})
		broadcast(Message{
			Type: joined,
			From: c.id,
		})

		select {

		case <-ctx.Done():
			cCancel()

		case <-cCtx.Done():
			broadcast(Message{
				Type: left,
				From: c.id,
			})
			csMx.Lock()
			delete(cs, c.id)
			csMx.Unlock()
		}

		wg.Wait()
	}))

	app.Use("/", fs.New(fs.Config{
		Root:         http.FS(ui.FS),
		RootPath:     "dist",
		Index:        "index.html",
		NotFoundFile: "index.html",
	}))

	bindAddr := os.Getenv("BIND_ADDR")

	if os.Getenv("USE_TLS") == "1" {
		if bindAddr == "" {
			bindAddr = ":8443"
		}
		go app.ListenTLS(bindAddr, "cert.pem", "key.pem")
	} else {
		if bindAddr == "" {
			bindAddr = ":8080"
		}
		go app.Listen(bindAddr)
	}

	exit := make(chan os.Signal)
	signal.Notify(exit, os.Interrupt, syscall.SIGTERM)
	<-exit

	cancel()
	app.Shutdown()
}
