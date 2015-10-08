package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	Namespace   = "dashboard"
	ConsulAddr  = "127.0.0.1:8500"
	Version     string
	ExtAssetDir string
	Nodes       []Node
	mutex       sync.RWMutex
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
	Warning
	Danger
	Info
)

func (s Status) MarshalText() ([]byte, error) {
	if s <= Danger {
		return []byte(strings.ToLower(s.String())), nil
	} else {
		return []byte(strconv.FormatInt(int64(s), 10)), nil
	}
}

type Item struct {
	Category  string `json:"category"`
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
		Timestamp: time.Unix(kv.Flags/1000, 0).Format("2006-01-02 15:04:05 -0700"),
	}
	item.Status = Status(kv.Flags % 1000)

	// kv.Key : {namespace}/{category}/{node}/{key}
	path := strings.Split(kv.Key, "/")
	item.Category = path[1]
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

type gzipResponseWriter struct {
	io.Writer
	http.ResponseWriter
}

func (w gzipResponseWriter) Write(b []byte) (int, error) {
	return w.Writer.Write(b)
}

func makeGzipHandler(fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			fn(w, r)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		defer gz.Close()
		gzr := gzipResponseWriter{Writer: gz, ResponseWriter: w}
		fn(gzr, r)
	}
}

func main() {
	var (
		port        int
		showVersion bool
		trigger     string
	)
	flag.StringVar(&Namespace, "namespace", Namespace, "Consul kv top level key name. (/v1/kv/{namespace}/...)")
	flag.IntVar(&port, "port", 3000, "http listen port")
	flag.StringVar(&ExtAssetDir, "asset", "", "Serve files located in /assets from local directory. If not specified, use built-in asset.")
	flag.BoolVar(&showVersion, "v", false, "show vesion")
	flag.BoolVar(&showVersion, "version", false, "show vesion")
	flag.StringVar(&trigger, "trigger", "", "trigger command")
	flag.Parse()

	if showVersion {
		fmt.Println("consul-kv-dashboard: version:", Version)
		return
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", makeGzipHandler(indexPage))
	mux.HandleFunc("/api/", makeGzipHandler(kvApiProxy))

	if ExtAssetDir != "" {
		mux.Handle("/assets/",
			http.StripPrefix("/assets/", http.FileServer(http.Dir(ExtAssetDir))))
	} else {
		mux.Handle("/assets/",
			http.FileServer(NewAssetFileSystem("/assets/")))
	}
	http.Handle("/", mux)

	log.Println("listen port:", port)
	log.Println("asset directory:", ExtAssetDir)
	log.Println("namespace:", Namespace)
	if trigger != "" {
		log.Println("trigger:", trigger)
		go watchForTrigger(trigger)
	}
	go updateNodeList()

	log.Fatal(http.ListenAndServe(":"+strconv.Itoa(port), nil))
}

func indexPage(w http.ResponseWriter, r *http.Request) {
	var (
		data []byte
		err  error
	)
	if ExtAssetDir == "" {
		data, err = Asset("index.html")
	} else {
		var f *os.File
		f, err = os.Open(ExtAssetDir + "/index.html")
		data, err = ioutil.ReadAll(f)
	}
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
			if itemInNodes(&item) {
				items = append(items, item)
			}
		}
		enc.Encode(items)
	}
}

func watchForTrigger(command string) {
	var index int64
	lastStatus := make(map[string]Status)
	for {
		resp, newIndex, err := callConsulAPI(
			"/v1/kv/" + Namespace + "/?recurse&wait=55s&index=" + strconv.FormatInt(index, 10),
		)
		if err != nil {
			log.Println("[error]", err)
			time.Sleep(10 * time.Second)
			continue
		}
		index = newIndex
		defer resp.Body.Close()
		var kvps []*KVPair
		dec := json.NewDecoder(resp.Body)
		dec.Decode(&kvps)

		currentItem := make(map[string]Item)
		for _, kv := range kvps {
			item := kv.NewItem()
			if !itemInNodes(&item) {
				continue
			}
			if _, exist := currentItem[item.Category]; !exist {
				currentItem[item.Category] = item
			} else if currentItem[item.Category].Status < item.Status {
				currentItem[item.Category] = item
			}
		}
		for category, item := range currentItem {
			if _, exist := lastStatus[category]; !exist {
				// at first initialze
				lastStatus[category] = item.Status
				log.Printf("[info] %s: status %s", category, item.Status)
			} else if lastStatus[category] != item.Status {
				// status changed. invoking trigger.
				log.Printf("[info] %s: status %s -> %s", category, lastStatus[category], item.Status)
				lastStatus[category] = item.Status
				b, _ := json.Marshal(item)
				err := invokePipe(command, bytes.NewReader(b))
				if err != nil {
					log.Println("[error]", err)
				}
			}
		}
		time.Sleep(1 * time.Second)
	}
}

func invokePipe(command string, src io.Reader) error {
	log.Println("[info] Invoking command:", command)
	cmd := exec.Command("sh", "-c", command)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	err = cmd.Start()
	if err != nil {
		return err
	}
	cmdCh := make(chan error)
	// src => stdin
	go func() {
		_, err := io.Copy(stdin, src)
		if err != nil {
			cmdCh <- err
		}
		stdin.Close()
	}()
	// wait for command exit
	go func() {
		cmdCh <- cmd.Wait()
	}()
	go io.Copy(os.Stdout, stdout)
	go io.Copy(os.Stderr, stderr)

	cmdErr := <-cmdCh
	return cmdErr
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

func itemInNodes(item *Item) bool {
	mutex.RLock()
	defer mutex.RUnlock()
	for _, node := range Nodes {
		if item.Node == node.Node {
			item.Address = node.Address
			return true
		}
	}
	return false
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
