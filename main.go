package main

import (
	"context"
	"fmt"
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

	"mooz/fs"
	"mooz/ui"
)

// Типы сообщений, которые будут передаваться между клиентом и сервером.
const (
	// Ping и pong сообщения необходимы для поддержания websocket соединения.
	ping = "ping"
	pong = "pong"

	// Cообщение о готовности websocket соединения.
	initialized = "initialized"

	// Cообщение о подключении нового клиента.
	joined = "joined"

	// Cообщение об отключении клиента.
	left = "left"
)

// Структура стандартного сообщения, которое будет передавться между
// клиентами и клиентом и сервером.
type Message struct {
	// ID клиента, которому направляется сообщение.
	To string `json:"to,omitempty"`
	// ID клиента, от которого отправляется сообщение.
	From string `json:"from,omitempty"`
	// Тип сообщения.
	Type string `json:"type"`
	// Какие-то данные, зависят от типа сообщения.
	Data interface{} `json:"data,omitempty"`
}

// Структура клиента.
type Client struct {
	// ID клиента.
	id string
	// Websocket подключение клиента.
	conn *websocket.Conn
	// Канал сообщений, которые будут отправляться клиенту.
	out chan Message
}

// Точка входа в сервер
func main() {

	// Создание fiber приложения.
	app := fiber.New(fiber.Config{
		ReadTimeout: 10 * time.Second,
	})

	// Подключение стандартных middleware.
	app.Use(recover.New(), logger.New(), cors.New())

	var (
		// Контекст для завершения работы сервера
		ctx, cancel = context.WithCancel(context.Background())
		// Хранилище клиентов.
		cs = map[string]Client{}
		// Mutex для конкурентного доступа к хранилищу клиентов.
		csMx sync.RWMutex
	)

	// Функция для отправки сообщений сразу всем клиентам.
	broadcast := func(m Message) {
		csMx.RLock()
		// Проходимся по всем клиентам.
		for id, c := range cs {
			// Если клиент - это клиент, который отправляет сообщение,
			// то пропускаем его.
			if id == m.From {
				continue
			}
			// Направляем сообщение клиенту.
			c.out <- m
		}
		csMx.RUnlock()
	}

	// Функция для отправки сообщения одному клиенту.
	send := func(m Message) {
		csMx.RLock()
		c, exists := cs[m.To]
		if exists {
			c.out <- m
		}
		csMx.RUnlock()
	}

	// Регистрация websocket-обработчика.
	app.Get("/ws", websocket.New(func(conn *websocket.Conn) {

		// Тело websocket-обработчика. Тут происходит вся магия по передаче
		// вспомогательных данных.

		// Создаём клиента.
		c := Client{
			id:   fmt.Sprintf("%p", conn),
			conn: conn,
			out:  make(chan Message),
		}

		// Безопасно кладём клиента в хранилище клиентов.
		csMx.Lock()
		cs[c.id] = c
		csMx.Unlock()

		var (
			// Контекст для завершения websocket-соединения.
			cCtx, cCancel = context.WithCancel(context.Background())

			// Wait group для ожидания завершения клиентских обработчиков.
			wg sync.WaitGroup

			// Wait group для ожидания старта клиентских обработчиков.
			startWg sync.WaitGroup

			// Хранилище данных для ping-pong. С помощью этих данных
			// поддерживается устойчивое websocket-соединение.
			pings = map[int64]struct{}{}

			// Mutex для конкурентного доступа к хранилищу данных ping-pong.
			pingsMx sync.Mutex
		)

		// Завершаем контекст websocket соединения при выходе из
		// websocket-обработчика.
		defer cCancel()

		// Запуск обработчика сообщений, которые будут отправляться клиенту.
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
				// Ждём очередное сообщение.
				case m = <-c.out:
				}

				// Отправляем сообщение клиенту.
				err := c.conn.WriteJSON(m)
				if err != nil {
					cCancel()
					return
				}
			}
		}()

		// Запуск обработчика ping.
		wg.Add(1)
		startWg.Add(1)
		go func() {
			defer wg.Done()
			startWg.Done()

			// Создаём тикер с периодом 3 секунды.
			t := time.NewTicker(3 * time.Second)
			defer t.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-cCtx.Done():
					return
				// Раз в 3 секунды
				case <-t.C:
				}

				pingsMx.Lock()
				// Если в хранилище ping-pong записано больше 5 записей, то
				// завершаем подключение клиента. Такое может произойти если
				// соединение не стабильное и часть записей теряется или
				// не доходят вовремя.
				if len(pings) > 5 {
					cCancel()
					return
				}
				// Получаем текущее время в unix timestamp.
				p := time.Now().Unix()
				// Записываем в хранилище ping-pong полученное время.
				pings[p] = struct{}{}
				pingsMx.Unlock()

				// Отправляем сообщение ping с полученным временем.
				send(Message{
					To:   c.id,
					Type: ping,
					Data: p,
				})
			}
		}()

		// Запуск обработчика сообщений, которые приходят от клиента.
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

				// Читаем очередное сообщение.
				err := c.conn.ReadJSON(&m)
				if err != nil {
					cCancel()
					return
				}

				// Смотрим тип сообщения.
				switch m.Type {

				// Если тип сообщения pong.
				case pong:
					// Достаём из данных время в unix timestamp.
					rp, ok := m.Data.(float64)
					if !ok {
						cCancel()
						return
					}

					pingsMx.Lock()
					// Проходимся по всем записям в хранилище ping-pong.
					for p := range pings {
						// Если очередная запись, время unix timestamp,
						// меньше равно полученного времени, то удаляем его из
						// хранилища.
						if p <= int64(rp) {
							delete(pings, p)
						}
					}
					pingsMx.Unlock()

				// Для всех остальных типов сообщений мы отправляем их адресату,
				// указав клиента-отправителя.
				default:
					m.From = c.id
					send(m)
				}
			}
		}()

		// Ожидание старта всех клиентских обработчиков.
		startWg.Wait()

		// Отправляем клиенту сообщение о готовности websocket-соединения.
		send(Message{
			Type: initialized,
			To:   c.id,
		})

		// Отправляем остальным клиентам сообщение о том что появился новый
		// клиент.
		broadcast(Message{
			Type: joined,
			From: c.id,
		})

		// Ожидание окончания контекстов клиента или сервера.
		select {

		// Если окончился контекст сервера.
		case <-ctx.Done():
			// Завершаем контекст клиента.
			cCancel()

		// Если окончился контекст клиента.
		case <-cCtx.Done():
			// Отправляем остальным клиентам сообщение о том, что клиент
			// вышел из чата.
			broadcast(Message{
				Type: left,
				From: c.id,
			})
			csMx.Lock()
			// Удаляем клиента из хранилища клиентов.
			delete(cs, c.id)
			csMx.Unlock()
		}

		// Ожидание завершения всех клиентских обработчиков.
		wg.Wait()
	}))

	// Регистрация обработчики статики (веб-страницы, javascript-файлов и
	// файлов стилей.)
	app.Use("/", fs.New(fs.Config{
		Root:         http.FS(ui.FS),
		RootPath:     "dist",
		Index:        "index.html",
		NotFoundFile: "index.html",
	}))

	// Получаем параметр адреса подключения веб-сервера.
	bindAddr := os.Getenv("LISTEN_ADDR")

	// Если мы будем использовать TLS.
	if os.Getenv("USE_TLS") == "1" {
		if bindAddr == "" {
			bindAddr = ":8443"
		}
		// Запускаем HTTPS-сервер.
		go app.ListenTLS(bindAddr, "cert.pem", "key.pem")

	} else { // Иначе.
		if bindAddr == "" {
			bindAddr = ":8080"
		}
		// Запускаем HTTP-сервер.
		go app.Listen(bindAddr)
	}

	// Ожидание сигнала завершения.
	exit := make(chan os.Signal)
	signal.Notify(exit, os.Interrupt, syscall.SIGTERM)
	<-exit

	// Завершаем контекст сервера.
	cancel()

	// Выключаем сервер.
	app.Shutdown()
}
