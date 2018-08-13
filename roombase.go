// Copyright (c) 2016 IBM Corp. All rights reserved.
// Use of this source code is governed by the Apache License,
// Version 2.0, a copy of which can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"net/http"
	"strings"
)

const (
	roomHello   = "roomHello"
	roomGoodbye = "roomGoodbye"
	lookAround  = "look"
)

// roomHello,43a4d07399ea23d648568c6d2d000b65,
// {"version": 1,"username": "DevUser","userId": "dummy.DevUser"}
type HelloMessage struct {
	Version  int    `json:"version,omitempty"`
	UserId   string `json:"userId,omitempty"`
	Username string `json:"username,omitempty"`
}

// roomGoodbye,43a4d07399ea23d648568c6d2d000b65,
// {"username": "DevUser","userId": "dummy.DevUser"}
type GoodbyeMessage struct {
	UserId   string `json:"userId,omitempty"`
	Username string `json:"username,omitempty"`
}

// room,43a4d07399ea23d648568c6d2d000b65,
// {"username":"DevUser","userId":"dummy.DevUser","content":"/examine book"}
type GameonRequest struct {
	UserId   string `json:"userId,omitempty"`
	Username string `json:"username,omitempty"`
	Content  string `json:"content,omitempty"`
}

var (
	ExpectedMessageType = websocket.TextMessage
	upgrader            = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
	// Supported versions is the list of version numbers
	// that we are willing to support. Currently we only
	// support version 1.
	SupportedVersions = []int{1}
)

// Handles incoming requests directed to our room.
//
// Well-formed requests are a comma-delimited triple, consisting
// of a command, a room name (ours), and a JSON string.
// For example:
//  "roomHello,ROOM.3100,{\"username\": \"DevUser\",\"userId\": \"dummy.DevUser\"}"
func roomHandler(w http.ResponseWriter, r *http.Request) {
	locus := "ROOM.HANDLER"
	checkpoint(locus, "BEGIN")

	// For debugging purposes
	fmt.Println("HERE: ", r.Header.Get("gameon-signature"))
	fmt.Println("HERE: ", r.Header.Get("gameon-date"))

	headers := getHandshakeHeader(r)
	fmt.Println(headers)

	w.Header().Set("gameon-signature", headers["gameon-signature"][0])
	w.Header().Set("gameon-date", headers["gameon-date"][0])

	conn, err := upgrader.Upgrade(w, r, headers)
	if err != nil {
		checkpoint(locus, fmt.Sprintf("WS.ERROR err=%s", err.Error()))
		checkpoint(locus, "BYE-BYE Room")
		conn.Close()
		return
	}
	ack(conn)

	for {
		checkpoint(locus, "READ We are waiting for a message.")
		_, payload, err := conn.ReadMessage()
		if err != nil {
			checkpoint(locus, fmt.Sprintf("UNREADABLE.MESSAGE err=%s", err.Error()))
			checkpoint(locus, "BYE-BYE Room")
			conn.Close()
			return
		}
		cmd, room, j, err := parseRequest(payload)
		if err != nil {
			checkpoint(locus, fmt.Sprintf("PARSE.ERROR err=%s", err.Error()))
			continue
		}

		if config.debug {
			checkpoint(locus, fmt.Sprintf("PARSE cmd=%s", cmd))
			checkpoint(locus, fmt.Sprintf("PARSE room=%s", room))
			checkpoint(locus, fmt.Sprintf("PARSE json=%s", j))
		}

		switch cmd {
		case "roomHello":
			var req HelloMessage
			err = json.Unmarshal([]byte(j), &req)
			if err != nil {
				checkpoint(locus, fmt.Sprintf("JSON.UNMARSHALL.ERROR err=%s", err.Error()))
				checkpoint(locus, fmt.Sprintf("JSON.UNMARSHALL.ERROR Offending JSON=%s", j))
				continue
			}
			err = handleHello(conn, &req, room)

		case "roomGoodbye":
			var req GoodbyeMessage
			err = json.Unmarshal([]byte(j), &req)
			if err != nil {
				checkpoint(locus, fmt.Sprintf("JSON.UNMARSHALL.ERROR err=%s", err.Error()))
				checkpoint(locus, fmt.Sprintf("JSON.UNMARSHALL.ERROR Offending JSON=%s", j))
				continue
			}
			err = handleGoodbye(conn, &req, room)

		case "room":
			var req GameonRequest
			err = json.Unmarshal([]byte(j), &req)
			if err != nil {
				checkpoint(locus, fmt.Sprintf("JSON.UNMARSHALL.ERROR err=%s", err.Error()))
				checkpoint(locus, fmt.Sprintf("JSON.UNMARSHALL.ERROR Offending JSON=%s", j))
				continue
			}
			err = handleRoom(conn, &req, room)
		default:
			err = handleInvalidMessage(conn, payload)
		}
		if err != nil {
			checkpoint(locus, fmt.Sprintf("HANDLING.ERROR err=%s", err.Error()))
		}
	}
}

// Returns the three components of a room request payload:
// command, room, and JSON payload. An error is also returned
// and the caller must discard any results if the error is not
// nil.
// The following checks are made. Any additional checking, such
// as JSON payload validation, are left to the caller.
// - There must at least three comma-delimted parts. Additional
//   commas after the first two are not inspected.
//   "id,name" is not okay
//   "id,name,{\"foo\"}"  is okay
//   "id,name,{\"foo\":\"one,two,three\"}" is okay
//   "id,name,{this is bad JSON" is, sadly, okay
// - The 2nd field, which is the target roomname, MUST match the
//   name we registered our room under.
func parseRequest(payload []byte) (c, r, j string, err error) {
	locus := "PARSE.REQ"
	s := string(payload)
	tokens := strings.SplitN(s, ",", 3)
	if config.debug {
		checkpoint(locus, s)
	}
	if len(tokens) != 3 {
		err = PayloadError{"Invalid request format."}
		return
	}
	c = tokens[0]
	r = tokens[1]
	j = tokens[2]
	checkpoint(locus, fmt.Sprintf("cmd=%s room=%s json=%s", c, r, j))
	return
}

func handleInvalidMessage(conn *websocket.Conn, p []byte) error {
	return PayloadError{fmt.Sprintf("Unrecognized command in payload '%s'", string(p))}
}

type WebSocketAck struct {
	// This is the list of versions that we are willing to support.
	Version []int `json:"version,omitempty"`
}

// Acknowledges the newly open websocket.
func ack(conn *websocket.Conn) (e error) {
	locus := "ACK"
	var ack WebSocketAck
	ack.Version = SupportedVersions
	j, e := json.MarshalIndent(ack, "", "    ")
	if e != nil {
		checkpoint(locus, fmt.Sprintf("FAILED. err=%s", e.Error()))
		return
	}
	var m = fmt.Sprintf("%s,%s", "ack", string(j))
	e = conn.WriteMessage(ExpectedMessageType, []byte(m))
	if config.debug {
		checkpoint(locus, fmt.Sprintf("MSG=%s", m))
	}
	if e != nil {
		checkpoint(locus, fmt.Sprintf("FAILED err=%s", e.Error()))
	}
	return
}
