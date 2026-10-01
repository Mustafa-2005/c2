# c2-lab

A minimal, educational Command & Control (C2) framework built for a university
security course: a **Go HTTP server** (listener + operator console) and a
**Rust agent** (implant) that beacons over plain HTTP/JSON.

> ⚠️ For coursework and lab use only. Run the agent exclusively on machines you
> own or have explicit written permission to test (your own VM, a lab network).

## Architecture

```
┌──────────────┐  1. POST /api/register   ┌───────────────┐
│              │ ───────────────────────► │               │
│  Rust agent  │  2. GET  /api/tasks      │   Go server   │◄─ operator console
│  (beacon)    │ ───────────────────────► │  (listener)   │   list / task /
│              │ ◄──── task or 204 ────── │               │   results / quit
│              │  3. POST /api/results    │               │
│              │ ───────────────────────► │               │
└──────────────┘   every POLL_SECS (3s)   └───────────────┘
```

The agent does not listen on any port — it only makes outbound HTTP requests
(a "beaconing" implant), so it works through NAT without port forwarding.

## Protocol (JSON over HTTP)

| Endpoint                | Direction | Body                                                     | Response                  |
|-------------------------|-----------|----------------------------------------------------------|---------------------------|
| `POST /api/register`    | agent → server | `{"id","hostname","user","os"}`                     | `{"id":"<agent id>"}`     |
| `GET /api/tasks?id=<id>`| agent → server | —                                                   | task JSON, or `204` if none |
| `POST /api/results`     | agent → server | `{"agent_id","task_id","output","success"}`         | `{"status":"ok"}`         |

The server keeps all state in memory: a map of agents, each with a pending
task queue and a list of results. Tasks are marked as sent on first pickup
(fire-and-forget: no acknowledgement/retry).

## Build

Requires Go 1.22+ and Rust (stable).

```bash
# server
cd server
go build -o c2-server.exe .

# agent
cd ../agent
cargo build --release
```

## Run (demo on one machine)

Terminal 1 — the operator:

```text
$ ./server/c2-server.exe
commands:
  list                    show registered agents
  task <agentID> <cmd>    queue a shell command for an agent
  results <agentID>       show output received from an agent
  quit                    stop the server

c2>
```

Terminal 2 — start an agent (defaults to `http://127.0.0.1:8080`):

```text
$ ./agent/target/release/agent.exe            # random server-assigned id
$ ./agent/target/release/agent.exe http://127.0.0.1:8080 agent01   # fixed id
```

Session example:

```text
c2> list
ID         HOSTNAME           USER    OS       LAST SEEN  PENDING
agent01    DESKTOP-XXXX       acer    windows  3s         0

c2> task agent01 whoami
queued task 1 for agent agent01

[result] agent agent01 task 1 (ok):
desktop-xxxx\acer

c2> task agent01 exit        # tells that agent to shut down
```

To test across machines, run the server on a reachable host and give the agent
its URL: `agent.exe http://192.168.1.10:8080`. Windows will likely prompt the
operator to allow the listener through the firewall.

## Code tour

- `server/main.go` — everything in one file: three HTTP handlers + a mutex-
  protected in-memory store + the interactive console (`console()`), plus a
  small goroutine (`resultPrinter`) that prints results as they arrive.
- `agent/src/main.rs` — register (with retry) → poll loop → `cmd /C` or
  `sh -c` execution → POST output back. `exit` as a task stops the agent.

## Deliberate simplifications (good "future work" talking points)

- **No encryption or authentication** — traffic is plain HTTP/JSON; anyone can
  spoof the API. Real C2 uses TLS + per-agent keys (e.g. HTTPS + mTLS or
  signed payloads).
- **No persistence or evasion** — the agent is a foreground process and does
  nothing to hide. This is what separates a lab project from malware.
- **Fire-and-forget tasking** — if the agent dies mid-task the task is lost;
  a real design would acknowledge receipt and re-queue.
- **In-memory state** — restart wipes agents/results; could add SQLite/JSON
  file persistence.
- **Fixed beacon interval** — real implants jitter the sleep and grow it over
  time to blend into traffic.
