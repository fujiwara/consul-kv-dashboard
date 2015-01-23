consul-kv-dashboard: *.go bindata.go
	stringer -type=Status
	go build

bindata.go: assets/index.html assets/scripts/dashboard.js
	go-bindata -prefix=assets assets/...
