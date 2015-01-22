consul-kv-dashboard: *.go bindata.go
	stringer -type=Status
	go build

bindata.go: public/* public/*/*
	go-bindata -prefix=public public/...
