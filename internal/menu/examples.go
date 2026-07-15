package menu

import "fmt"

const exampleForeignServer = `# SCENARIO 1 - FOREIGN server (exit anchor)
# Client in Iran forwards local ports out through this server.
[log]
level = "info"

[server]
bind_addr = "0.0.0.0:443"
transport = "tls"
token = "PUT-A-LONG-RANDOM-TOKEN-HERE"
allow_forward = true          # the server dials targets the client requests
# forward_whitelist = ["127.0.0.1:1194", "1.1.1.1:22"]  # optional allow-list

[server.tls]
self_name = "www.example.com"
cert = ""                     # empty => auto self-signed
key  = ""

[server.mux]
enable = true
keepalive_seconds = 10
max_stream_buffer  = 8388608
max_receive_buffer = 33554432
`

const exampleIranClientForward = `# SCENARIO 1 - IRAN client (forwards chosen ports to the foreign server)
[log]
level = "info"

[client]
server_addr = "FOREIGN_IP:443"
transport   = "tls"
token       = "PUT-A-LONG-RANDOM-TOKEN-HERE"
sni         = "www.example.com"
fingerprint = "chrome"
insecure    = true            # self-signed server cert
pool_size   = 3

[client.mux]
enable = true
keepalive_seconds = 10
max_stream_buffer  = 8388608
max_receive_buffer = 33554432

[client.reconnect]
min_millis = 500
max_millis = 30000

# Pick which local ports get tunneled to the foreign server:
[[client.forwards]]
name        = "openvpn"
local_addr  = "0.0.0.0:1194"   # users in Iran connect here
target_addr = "127.0.0.1:1194" # the foreign server dials this

[[client.forwards]]
name        = "ssh"
local_addr  = "0.0.0.0:2222"
target_addr = "1.1.1.1:22"
`

const exampleIranServerReverse = `# SCENARIO 2 - IRAN server (relay only; exposes public ports)
[log]
level = "info"

[server]
bind_addr = "0.0.0.0:443"
transport = "wss"
token     = "PUT-A-LONG-RANDOM-TOKEN-HERE"
ws_path   = "/cando"
host      = "www.example.com"

[server.tls]
self_name = "www.example.com"

[server.mux]
enable = true

# Public ports Iranian users connect to; each maps to a foreign-client target.
[[server.services]]
name      = "proxy"
bind_addr = "0.0.0.0:8388"

[[server.services]]
name      = "web"
bind_addr = "0.0.0.0:8080"
`

const exampleForeignClientReverse = `# SCENARIO 2 - FOREIGN client (holds the real services; Iran only relays)
[log]
level = "info"

[client]
server_addr = "IRAN_IP:443"
transport   = "wss"
token       = "PUT-A-LONG-RANDOM-TOKEN-HERE"
sni         = "www.example.com"
host        = "www.example.com"
ws_path     = "/cando"
fingerprint = "chrome"
insecure    = true
pool_size   = 3

[client.mux]
enable = true

[client.reconnect]
min_millis = 500
max_millis = 30000

# Local targets matching the server's service names:
[[client.services]]
name       = "proxy"
local_addr = "127.0.0.1:8388"

[[client.services]]
name       = "web"
local_addr = "127.0.0.1:8080"
`

func printExamples() {
	fmt.Print("\n================  SCENARIO 1 (client in Iran, forward)  ================\n")
	fmt.Print("--- foreign server ---\n")
	fmt.Print(exampleForeignServer)
	fmt.Print("--- iran client ---\n")
	fmt.Print(exampleIranClientForward)
	fmt.Print("\n================  SCENARIO 2 (client abroad, Iran relays)  ================\n")
	fmt.Print("--- iran server ---\n")
	fmt.Print(exampleIranServerReverse)
	fmt.Print("--- foreign client ---\n")
	fmt.Print(exampleForeignClientReverse)
}
