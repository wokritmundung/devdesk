// rewarmctl is the M1 manual trigger: drive checkpoint/restore against a
// node agent by hand. The preemption hook (M2) automates exactly these calls.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"

	"github.com/wokritmundung/devdesk/pkg/api"
)

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  rewarmctl [-agent URL] checkpoint <podUID> <engine-native|process> [engineURL|pid]
  rewarmctl [-agent URL] restore <snapshotID> [engineURL]
  rewarmctl [-agent URL] list`)
	os.Exit(2)
}

func main() {
	agentURL := "http://127.0.0.1:8484"
	args := os.Args[1:]
	if len(args) >= 2 && args[0] == "-agent" {
		agentURL, args = args[1], args[2:]
	}
	if len(args) < 1 {
		usage()
	}
	switch args[0] {
	case "checkpoint":
		if len(args) < 3 {
			usage()
		}
		req := api.CheckpointRequest{PodUID: args[1], Mode: api.Mode(args[2])}
		if len(args) > 3 {
			if pid, err := strconv.Atoi(args[3]); err == nil && req.Mode == api.ModeProcess {
				req.ProcessPID = pid
			} else {
				req.EngineURL = args[3]
			}
		}
		post(agentURL+"/v1/checkpoint", req)
	case "restore":
		if len(args) < 2 {
			usage()
		}
		req := api.RestoreRequest{SnapshotID: args[1]}
		if len(args) > 2 {
			req.EngineURL = args[2]
		}
		post(agentURL+"/v1/restore", req)
	case "list":
		resp, err := http.Get(agentURL + "/v1/snapshots")
		exitOn(err)
		defer resp.Body.Close()
		io.Copy(os.Stdout, resp.Body)
	default:
		usage()
	}
}

func post(url string, v any) {
	b, _ := json.Marshal(v)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	exitOn(err)
	defer resp.Body.Close()
	io.Copy(os.Stdout, resp.Body)
	fmt.Println()
	if resp.StatusCode >= 300 {
		os.Exit(1)
	}
}

func exitOn(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
