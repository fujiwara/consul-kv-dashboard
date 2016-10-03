GIT_VER := $(shell git describe --tags)

.PHONY: packages clean

consul-kv-dashboard: dashboard.go bindata.go
	stringer -type=Status
	go build

bindata.go: assets/index.html assets/scripts/dashboard.js assets/css/style.css
	go-bindata -prefix=assets assets/...

packages: bindata.go dashboard.go
	gox -os="linux darwin windows" -arch="amd64 386" -output "pkg/{{.Dir}}-${GIT_VER}-{{.OS}}-{{.Arch}}" -ldflags "-X main.Version=${GIT_VER}"
	cd pkg && find . -name "*${GIT_VER}*" -type f -exec zip {}.zip {} \;

clean:
	rm -fr pkg/* consul-kv-dashboard
