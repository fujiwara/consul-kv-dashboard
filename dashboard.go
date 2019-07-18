package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
)

var (
	Namespace    = "dashboard"
	DBConnection = dynamodb.New(session.New())
	Version      string
	ExtAssetDir  string
	Nodes        []Node
	Services     map[string][]string
	mutex        sync.RWMutex
)

type DynamoDBItem struct {
	Category  string
	NodeKey   string
	Address   string
	Timestamp int64
	Status    Status
	Data      string
}

type Status int64

const (
	Success Status = iota
	Warning
	Danger
	Info
)

func (s Status) MarshalText() ([]byte, error) {
	if s <= Info {
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

type Node struct {
	Node    string
	Address string
}

func (dbItem *DynamoDBItem) NewItem() Item {
	item := Item{
		Category:  dbItem.Category,
		Address:   dbItem.Address,
		Timestamp: time.Unix(dbItem.Timestamp, 0).Format("2006-01-02 15:04:05 -0700"),
		Status:    dbItem.Status,
		Data:      dbItem.Data,
	}
	nodeKey := strings.Split(dbItem.NodeKey, "/")
	item.Node = nodeKey[0]
	if len(nodeKey) >= 2 {
		item.Key = nodeKey[1]
	}
	return item

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
	/*
		if trigger != "" {
			log.Println("trigger:", trigger)
			go watchForTrigger(trigger)
		}
	*/
	// go updateNodes()
	// go updateServices()

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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, string(data))
}

func kvApiProxy(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	enc := json.NewEncoder(w)
	if _, t := r.Form["keys"]; t {
		categories, err := getDynamoDBCategories()
		if err != nil {
			log.Println(err)
		}
		log.Println("keys:", categories)
		enc.Encode(categories)
	} else {
		category := strings.TrimPrefix(r.URL.Path, "/api/")
		dbItems, err := getDynamoDBItems(category)
		if err != nil {
			log.Println(err)
		}
		items := make([]Item, 0, len(dbItems))
		for _, dbItem := range dbItems {
			item := dbItem.NewItem()
			items = append(items, item)
		}
		log.Printf("[%s] item num: %d", category, len(items))
		enc.Encode(items)
	}
}

func getDynamoDBItems(category string) ([]*DynamoDBItem, error) {
	//aws dynamodb api request
	input := &dynamodb.QueryInput{
		ExpressionAttributeValues: map[string]*dynamodb.AttributeValue{
			":categoryName": {
				S: aws.String(category),
			},
		},
		KeyConditionExpression: aws.String("category = :categoryName"),
		TableName:              aws.String(Namespace),
	}

	result, err := DBConnection.Query(input)
	if err != nil {
		DynamoDBConnectionErrorLog(err)
		return nil, err
	}

	var dbItems []*DynamoDBItem
	for _, dbItemMap := range (*result).Items {
		dbItem := DynamoDBItem{
			Category: *dbItemMap["category"].S,
			NodeKey:  *dbItemMap["node_key"].S,
		}
		if dbItemMap["address"] != nil {
			if dbItemMap["address"].S != nil {
				dbItem.Address = *dbItemMap["address"].S
			}
		}
		if dbItemMap["data"] != nil {
			if dbItemMap["data"].S != nil {
				dbItem.Data = *dbItemMap["data"].S
			}
		}
		if dbItemMap["timestamp"] != nil {
			if dbItemMap["timestamp"].N != nil {
				i, err := strconv.ParseInt(*dbItemMap["timestamp"].N, 10, 64)
				if err != nil {
					log.Println(err)
				}
				dbItem.Timestamp = i
			}
		}
		if dbItemMap["status"] != nil {
			if dbItemMap["status"].N != nil {
				i, err := strconv.Atoi(*dbItemMap["status"].N)
				if err != nil {
					log.Println(err)
				}
				dbItem.Status = (Status)(i)
			}
		}
		dbItems = append(dbItems, &dbItem)
	}
	return dbItems, nil

}

func getDynamoDBCategories() ([]string, error) {
	input := &dynamodb.ScanInput{
		ProjectionExpression: aws.String("category"),
		TableName:            aws.String(Namespace),
	}

	result, err := DBConnection.Scan(input)
	if err != nil {
		DynamoDBConnectionErrorLog(err)
		return nil, err
	}

	var categories []string
	for _, dbItem := range (*result).Items {
		if dbItem["category"] != nil {
			if !isInSlice(categories, *dbItem["category"].S) {
				categories = append(categories, *dbItem["category"].S)
			}
		}
	}

	return categories, nil
}

func isInSlice(slice []string, pattern string) bool {
	for _, i := range slice {
		if i == pattern {
			return true
		}
	}
	return false
}

func DynamoDBConnectionErrorLog(err error) error {
	if aerr, ok := err.(awserr.Error); ok {
		switch aerr.Code() {
		case dynamodb.ErrCodeProvisionedThroughputExceededException:
			log.Println(dynamodb.ErrCodeProvisionedThroughputExceededException, aerr.Error())
		case dynamodb.ErrCodeResourceNotFoundException:
			log.Println(dynamodb.ErrCodeResourceNotFoundException, aerr.Error())
		case dynamodb.ErrCodeRequestLimitExceeded:
			log.Println(dynamodb.ErrCodeRequestLimitExceeded, aerr.Error())
		case dynamodb.ErrCodeInternalServerError:
			log.Println(dynamodb.ErrCodeInternalServerError, aerr.Error())
		default:
			log.Println(aerr.Error())
		}
	} else {
		// Print the error, cast err to awserr.Error to get the Code and
		// Message from an error.
		log.Println(err.Error())
	}
	return err
}

/*
func watchForTrigger(command string) {
	var index int64
	lastStatus := make(map[string]Status)
	prevItem := make(map[Item]Status)
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
		var kvps []*KVPair
		dec := json.NewDecoder(resp.Body)
		dec.Decode(&kvps)
		resp.Body.Close()

		// find each current item of category
		currentItem := make(map[string]Item)
		for _, kv := range kvps {
			item := kv.NewItem()
			if !itemInCatalog(&item) {
				continue
			}

			current := compactItem(item)
			_, exist := prevItem[current]
			if exist && prevItem[current] != item.Status {
				currentItem[item.Category] = item
			}
		}
		for _, kv := range kvps {
			item := kv.NewItem()
			if !itemInCatalog(&item) {
				continue
			}
			if _, exist := currentItem[item.Category]; !exist {
				currentItem[item.Category] = item
			} else if currentItem[item.Category].Status < item.Status {
				currentItem[item.Category] = item
			}
		}

		// invoke trigger when a category status was changed
		for category, item := range currentItem {
			if _, exist := lastStatus[category]; !exist {
				// at first initialize
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

		// update previous item status
		for _, kv := range kvps {
			item := kv.NewItem()
			prev := compactItem(item)
			prevItem[prev] = item.Status
		}

		time.Sleep(1 * time.Second)
	}
}

// compactItem builds `Item` struct that has only `Category`, `Key`, and `Node` fields.
func compactItem(item Item) Item {
	return Item{
		Key:      item.Key,
		Category: item.Category,
		Node:     item.Node,
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
*/
