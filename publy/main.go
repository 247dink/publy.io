package main

import (
	"os"
	"fmt"
	"strconv"
	"log/slog"
	"net/http"
	"strings"
	"errors"
	"time"
	"context"
	"io/ioutil"

	"github.com/coder/websocket"
)

type channel struct {
	Name string
	Listeners []chan string
}

func (c channel) Send(message string) error {
	slog.Debug("Sending message", "message", message, "clients", len(c.Listeners))

	for i := range c.Listeners {
		c.Listeners[i]<-message
	}

	return nil
}

var CHANNELS = map[string]*channel{}

func getOrCreateChannel(path string) (*channel, error) {
	name := strings.Trim(path, "/")

	if strings.Contains(name, "/") {
		return nil, errors.New("Channel name cannot contain /")
	}
	if len(name) < 16 {
		return nil, errors.New("Channel name must be at least 16 chars")
	}
	_, ok := CHANNELS[name]
	if !ok {
		CHANNELS[name] = &channel{Name: name}
	}

	return CHANNELS[name], nil
}

func isWebsocketRequest(r *http.Request) bool {
	if r.Method != "GET" {
		return false
	}

	_, ok := r.Header["Upgrade"]
	if !ok {
		return false
	}

	_, ok = r.Header["Connection"]
	if !ok {
		return false
	}

	return true
}

func handleWebsocket(w http.ResponseWriter, r *http.Request, channel *channel) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer c.CloseNow()

	ctx, cancel := context.WithTimeout(r.Context(), time.Minute * 10)
	defer cancel()

	ctx = c.CloseRead(ctx)

	queue := make(chan string)
	channel.Listeners = append(channel.Listeners, queue)

	for {
		message := <-queue
		err = c.Write(ctx, websocket.MessageText, []byte(message))
		if err != nil {
			return
		}
	}
}

func handleDispatch(r *http.Request, channel *channel) error {
	payload := ""

	if r.Method == "GET" {
		payload = r.URL.RawQuery

	} else {
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			slog.Error("Error reading body", "err", err)
		} else {
			payload = string(body)
		}
	}

	return channel.Send(payload)
}

func parseArgs() (string, int) {
	host := "127.0.0.1"
	port := 8000
	argv := os.Args[1:]
	var err error

	for i := range(argv) {
		switch flag := argv[i]; flag {
		case "-h":
			host = argv[i + 1]
			break

		case "-p":
			port, err = strconv.Atoi(argv[i + 1])
			if err != nil {
				panic(fmt.Sprintf("Invalid port: %s, %s", argv[i + 1], err.Error()))
			}
			break
		}
	}

	return host, port
}

func main() {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request){
		// Parse channel from path.
		channel, err := getOrCreateChannel(r.URL.Path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		slog.Debug("Received request", "method", r.Method, "channel", channel.Name)

		// Check if websocket (has Upgrade and Connection headers)
		if isWebsocketRequest(r) {
			slog.Debug("Handling websocket")
			handleWebsocket(w, r, channel)
			return
		}

		slog.Debug("Handling dispatch")
		err = handleDispatch(r, channel)
		if err != nil {
			slog.Error("Error dispatching message", "err", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
	})

	slog.SetLogLoggerLevel(slog.LevelDebug)

	host, port := parseArgs()
	addr := fmt.Sprintf("%s:%d", host, port)
	slog.Info("Listening at", "address", addr)
	err := http.ListenAndServe(addr, handler)
	if err != nil {
		slog.Error("Could not start server", "err", err)
	}
}
