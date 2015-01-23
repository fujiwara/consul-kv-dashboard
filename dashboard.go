package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	Namespace  = "dashboard"
	ConsulAddr = "127.0.0.1:8500"
	Version    string
	Nodes      []Node
	mutex      sync.Mutex
)

type KVPair struct {
	Key         string
	CreateIndex int64
	ModifyIndex int64
	LockIndex   int64
	Flags       int64
	Value       []byte
}

type Status int64

const (
	Success Status = iota
	Info
	Warning
	Danger
)

func (s Status) MarshalText() ([]byte, error) {
	if s <= Danger {
		return []byte(strings.ToLower(s.String())), nil
	} else {
		return []byte(strconv.FormatInt(int64(s), 10)), nil
	}
}

type Item struct {
	Node      string `json:"node"`
	Address   string `json:"address"`
	Timestamp string `json:"timestamp"`
	Status    Status `json:"status"`
	Key       string `json:"key"`
	Data      string `json:"data"`
}

func (kv *KVPair) NewItem() Item {
	item := Item{
		Data:      string(kv.Value),
		Timestamp: time.Unix(kv.Flags/1000, 0).Format(time.RFC3339),
	}
	item.Status = Status(kv.Flags % 1000)

	// kv.Key : {namespace}/{category}/{node}/{key}
	path := strings.Split(kv.Key, "/")
	if len(path) >= 3 {
		item.Node = path[2]
	}
	if len(path) >= 4 {
		item.Key = path[3]
	}
	return item
}

type Node struct {
	Node    string
	Address string
}

func main() {
	var (
		port        int
		assetDir    string
		showVersion bool
	)
	flag.StringVar(&Namespace, "namespace", Namespace, "Consul kv top level key name. (/v1/kv/{namespace}/...)")
	flag.IntVar(&port, "port", 3000, "http listen port")
	flag.StringVar(&assetDir, "asset", "", "Serve files located in /assets from local directory. If not specified, use built-in asset.")
	flag.BoolVar(&showVersion, "v", false, "show vesion")
	flag.BoolVar(&showVersion, "version", false, "show vesion")
	flag.Parse()

	if showVersion {
		fmt.Println("consul-kv-dashboard: version:", Version)
		return
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", indexPage)
	mux.HandleFunc("/api/", kvApiProxy)

	if assetDir != "" {
		mux.Handle("/assets/",
			http.StripPrefix("/assets/", http.FileServer(http.Dir(assetDir))))
	} else {
		mux.Handle("/assets/",
			http.FileServer(NewAssetFileSystem("/assets/")))
	}
	http.Handle("/", mux)

	go updateNodeList()
	log.Println("listen port:", port)
	log.Println("asset directory:", assetDir)
	log.Println("namespace:", Namespace)
	log.Fatal(http.ListenAndServe(":"+strconv.Itoa(port), nil))
}

func indexPage(w http.ResponseWriter, r *http.Request) {
	data, err := Asset("index.html")
	if err != nil {
		log.Println(err)
	}
	fmt.Fprint(w, string(data))
}

func kvApiProxy(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	path := strings.TrimLeft(r.URL.Path, "/api")
	resp, _, err := callConsulAPI(
		"/v1/kv/" + Namespace + "/" + path + "?" + r.URL.RawQuery,
	)
	if err != nil {
		http.Error(w, fmt.Sprintf("%s", err), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, "[]", resp.StatusCode)
		return
	}
	if resp.StatusCode != http.StatusOK {
		http.Error(w, "", resp.StatusCode)
		io.Copy(w, resp.Body)
		return
	}
	// copy response header to client
	for name, value := range resp.Header {
		if strings.HasPrefix(name, "X-") || name == "Content-Type" {
			for _, v := range value {
				w.Header().Set(name, v)
			}
		}
	}

	// keys or values
	dec := json.NewDecoder(resp.Body)
	enc := json.NewEncoder(w)
	if _, t := r.Form["keys"]; t {
		var keys []string
		uniqKeyMap := make(map[string]bool)
		dec.Decode(&keys)
		for _, key := range keys {
			path := strings.Split(key, "/")
			if len(path) >= 2 {
				uniqKeyMap[path[1]] = true
			}
		}
		uniqKeys := make([]string, 0, len(uniqKeyMap))
		for key, _ := range uniqKeyMap {
			uniqKeys = append(uniqKeys, key)
		}
		sort.Strings(uniqKeys)
		enc.Encode(uniqKeys)
	} else {
		var kvps []*KVPair
		dec.Decode(&kvps)
		items := make([]Item, 0, len(kvps))
		for _, kv := range kvps {
			item := kv.NewItem()
			mutex.Lock()
			for _, node := range Nodes {
				if item.Node == node.Node {
					item.Address = node.Address
					items = append(items, item)
					break
				}
			}
			mutex.Unlock()
		}
		enc.Encode(items)
	}
}

func updateNodeList() {
	var index int64
	for {
		resp, newIndex, err := callConsulAPI(
			"/v1/catalog/nodes?index=" + strconv.FormatInt(index, 10) + "&wait=55s",
		)
		if err != nil {
			log.Println("[error]", err)
			time.Sleep(10 * time.Second)
			continue
		}
		index = newIndex
		defer resp.Body.Close()
		dec := json.NewDecoder(resp.Body)
		mutex.Lock()
		dec.Decode(&Nodes)
		log.Println("[info]", Nodes)
		mutex.Unlock()
		time.Sleep(1 * time.Second)
	}
}

func callConsulAPI(path string) (*http.Response, int64, error) {
	var index int64
	_url := "http://" + ConsulAddr + path
	log.Println("[info] get", _url)
	resp, err := http.Get(_url)
	if err != nil {
		log.Println("[error]", err)
		return nil, index, err
	}
	_indexes := resp.Header["X-Consul-Index"]
	if len(_indexes) > 0 {
		index, _ = strconv.ParseInt(_indexes[0], 10, 64)
	}
	return resp, index, nil
}
