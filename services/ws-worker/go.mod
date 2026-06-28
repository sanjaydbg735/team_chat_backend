// Module teamchat/ws-worker is the stateful WebSocket streaming service.
// It depends on teamchat/shared and on gorilla/websocket for the socket layer.
// See the replace directive note in the api-server go.mod.
module teamchat/ws-worker

go 1.19

require (
	github.com/go-sql-driver/mysql v1.7.1
	github.com/gorilla/websocket v1.5.3
	github.com/redis/go-redis/v9 v9.0.5
	teamchat/shared v0.0.0
)

require (
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
)

replace teamchat/shared => ../../shared
