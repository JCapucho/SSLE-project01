module ssle/prom-sd-observer

go 1.25.4

replace ssle/services => ../services

replace ssle/node-utils => ../node-utils

require (
	github.com/caarlos0/env/v11 v11.3.1
	ssle/node-utils v1.0.0
	ssle/services v1.0.0
)

require (
	golang.org/x/net v0.47.0 // indirect
	golang.org/x/sys v0.38.0 // indirect
	golang.org/x/text v0.31.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20251029180050-ab9386a59fda // indirect
	google.golang.org/grpc v1.78.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)
