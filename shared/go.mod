// Module teamchat/shared holds code reused by every TeamChat service:
// domain models, configuration, repositories, the pub/sub broker, and the
// Snowflake ID generator.  Services import this module; it imports none of them.
module teamchat/shared

go 1.19

require (
	github.com/go-sql-driver/mysql v1.7.1
	github.com/redis/go-redis/v9 v9.0.5
)

require (
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
)
