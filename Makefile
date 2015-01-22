consul-kv-dashboard: *.go
	stringer -type=Status
	go build

bindata.go: public/* public/*/*
	go-bindata -prefix=public public/...
