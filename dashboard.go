package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/dynamodbstreams"
)

var (
	Namespace      = "dashboard"
	DBConn         = dynamodb.New(session.New())
	StreamConn     = dynamodbstreams.New(session.New())
	StreamCh       = make(map[string]chan bool)
	StreamChMu     sync.Mutex
	TriggerCh      = make(chan Item)
	Version        string
	ExtAssetDir    string
	Trigger        string
	StreamInterval = time.Second
)

type Status int64

const (
	Success Status = iota
	Warning
	Danger
	Info
	Unknown
)

type DynamoDBItem struct {
	Category  string
	NodeKey   string
	Address   string
	Timestamp int64
	Status    Status
	Data      string
}

func NewDynamoDBItem(dbItemMap map[string]*dynamodb.AttributeValue) DynamoDBItem {
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

	dbItem.Status = Unknown
	if dbItemMap["status"] != nil {
		if dbItemMap["status"].N != nil {
			i, err := strconv.Atoi(*dbItemMap["status"].N)
			if err != nil {
				log.Println(err)
			} else {
				if Success <= (Status)(i) && (Status)(i) <= Unknown {
					dbItem.Status = (Status)(i)
				}
			}
		}
	}
	return dbItem
}

func (s Status) MarshalText() ([]byte, error) {
	if s <= Unknown {
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
	)
	flag.StringVar(&Namespace, "namespace", Namespace, "Consul kv top level key name. (/v1/kv/{namespace}/...)")
	flag.IntVar(&port, "port", 3000, "http listen port")
	flag.StringVar(&ExtAssetDir, "asset", "", "Serve files located in /assets from local directory. If not specified, use built-in asset.")
	flag.BoolVar(&showVersion, "v", false, "show vesion")
	flag.BoolVar(&showVersion, "version", false, "show vesion")
	flag.StringVar(&Trigger, "trigger", "", "trigger command")
	flag.Parse()

	if showVersion {
		fmt.Println("consul-kv-dashboard: version:", Version)
		return
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", makeGzipHandler(indexPage))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		log.Println("[info] HandleFunc")
		makeGzipHandler(kvApiProxy)(w, r)
	})

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

	catList, err := getDBCategories()
	if err != nil {
		log.Println(err)
	}
	for _, cat := range catList {
		StreamChMu.Lock()
		StreamCh[cat] = make(chan bool)
		StreamChMu.Unlock()
	}

	go DBUpdateWatch()

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
		log.Println("[info] invoke kvApiProxy / keys")
		categories, err := getDBCategories()
		if err != nil {
			log.Println(err)
			http.Error(w, fmt.Sprintf("%s", err), http.StatusInternalServerError)
		}
		log.Println("keys:", categories)
		enc.Encode(categories)
	} else {
		log.Println("[info] invoke kvApiProxy / items")
		category := strings.TrimPrefix(r.URL.Path, "/api/")
		if _, u := r.Form["update"]; u {
			log.Println("[info] request update")
			timer := time.After(time.Second * 55)
			StreamChMu.Lock()
			_, ok := StreamCh[category]
			StreamChMu.Unlock()
			if ok {
				ch := StreamCh[category]
				log.Println("[info] unlock,", ch)
				select {
				case <-ch:
					log.Println("[info] category data update")
				case <-timer:
					log.Println("[info] timeout")
				}
			} else {
				log.Println("[err] Stream category channel not found")
			}
		}

		log.Println("[info] getDBItems")
		dbItems, err := getDBItems(category)
		if err != nil {
			log.Println(err)
			http.Error(w, fmt.Sprintf("%s", err), http.StatusInternalServerError)
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

func getDBItems(category string) ([]*DynamoDBItem, error) {
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

	result, err := DBConn.Query(input)
	if err != nil {
		DynamoDBConnectionErrorLog(err)
		return nil, err
	}

	var dbItems []*DynamoDBItem
	for _, dbItemMap := range (*result).Items {
		dbItem := NewDynamoDBItem(dbItemMap)
		dbItems = append(dbItems, &dbItem)
	}
	return dbItems, nil

}

func getDBCategories() ([]string, error) {
	input := &dynamodb.ScanInput{
		ProjectionExpression: aws.String("category"),
		TableName:            aws.String(Namespace),
	}

	result, err := DBConn.Scan(input)
	if err != nil {
		DynamoDBConnectionErrorLog(err)
		return nil, err
	}

	categoriesMap := make(map[string]bool)
	for _, dbItem := range (*result).Items {
		if !categoriesMap[*dbItem["category"].S] {
			categoriesMap[*dbItem["category"].S] = true
		}
	}

	var categories []string
	for key, _ := range categoriesMap {
		categories = append(categories, key)
	}

	return categories, nil
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

func DBUpdateWatch() {
	input := &dynamodbstreams.ListStreamsInput{
		TableName: &Namespace,
	}
	result, err := StreamConn.ListStreams(input)
	if err != nil {
		StreamConnErrLog(err)
		return
	}
	if result.Streams == nil {
		log.Println("Stream not found")
		return
	}
	var arnList []*string
	for _, stream := range result.Streams {
		if stream.StreamArn != nil {
			arnList = append(arnList, stream.StreamArn)
		}
	}
	log.Println("[info] Stream ARN num: ", len(arnList))
	for _, arn := range arnList {
		go DBStreamDescribe(*arn)
	}

	// Watch for trigger command
	if Trigger != "" {
		log.Println("trigger:", Trigger)
		go watchForTrigger(Trigger)
	}
}

func DBStreamDescribe(arn string) {

	input := &dynamodbstreams.DescribeStreamInput{
		StreamArn: aws.String(arn),
	}
	result, err := StreamConn.DescribeStream(input)
	if err != nil {
		StreamConnErrLog(err)
		return
	}

	// TODO: nilチェック
	log.Println("[info] shards: ", len((*result).StreamDescription.Shards))
	for _, shard := range result.StreamDescription.Shards {
		go StreamShardReader(arn, *(shard).ShardId)
	}
}

func StreamShardReader(arn string, id string) {
	shardIteratorInput := &dynamodbstreams.GetShardIteratorInput{
		ShardId:           aws.String(id),
		ShardIteratorType: aws.String("LATEST"),
		StreamArn:         aws.String(arn),
	}
	shardIterator, err := StreamConn.GetShardIterator(shardIteratorInput)
	if err != nil {
		StreamConnErrLog(err)
		return
	}

	if shardIterator.ShardIterator == nil {
		log.Println("[err] shardIterator is nil")
		return
	}
	itr := shardIterator.ShardIterator

	var record *dynamodbstreams.GetRecordsOutput
	for {
		getRecordInput := &dynamodbstreams.GetRecordsInput{
			ShardIterator: aws.String(*itr),
		}
		record, err = StreamConn.GetRecords(getRecordInput)
		if err != nil {
			log.Println(err)
			return
		}
		if (*record).Records != nil && len(record.Records) > 0 {
			log.Println("[info] DB update")
			cat := *record.Records[0].Dynamodb.Keys["category"].S
			StreamChMu.Lock()
			if _, ok := StreamCh[cat]; ok {
				close(StreamCh[cat])
			}
			StreamCh[cat] = make(chan bool)
			StreamChMu.Unlock()

			if Trigger != "" {
				dbitem := NewDynamoDBItem(record.Records[0].Dynamodb.NewImage)
				TriggerCh <- dbitem.NewItem()
			}

		}
		if record.NextShardIterator == nil {
			log.Println("[info] Shard closed")
			return
		}
		itr = record.NextShardIterator
		time.Sleep(StreamInterval)
	}

}

func StreamConnErrLog(err error) error {
	if aerr, ok := err.(awserr.Error); ok {
		switch aerr.Code() {
		case dynamodbstreams.ErrCodeResourceNotFoundException:
			fmt.Println(dynamodbstreams.ErrCodeResourceNotFoundException, aerr.Error())
		case dynamodbstreams.ErrCodeLimitExceededException:
			fmt.Println(dynamodbstreams.ErrCodeLimitExceededException, aerr.Error())
		case dynamodbstreams.ErrCodeInternalServerError:
			fmt.Println(dynamodbstreams.ErrCodeInternalServerError, aerr.Error())
		case dynamodbstreams.ErrCodeExpiredIteratorException:
			fmt.Println(dynamodbstreams.ErrCodeExpiredIteratorException, aerr.Error())
		case dynamodbstreams.ErrCodeTrimmedDataAccessException:
			fmt.Println(dynamodbstreams.ErrCodeTrimmedDataAccessException, aerr.Error())
		default:
			fmt.Println(aerr.Error())
		}
	} else {
		// Print the error, cast err to awserr.Error to get the Code and
		// Message from an error.
		fmt.Println(err.Error())
	}
	return err
}

func watchForTrigger(command string) {
	// invoke trigger when a category status was changed
	var item Item
	for {
		select {
		case item = <-TriggerCh:
		}

		// status changed. invoking trigger.
		//TODO: 変更前のstatusを持ってくる
		//log.Printf("[info] %s: status %s -> %s", category, lastStatus[category], item.Status)
		b, _ := json.Marshal(item)
		err := invokePipe(command, bytes.NewReader(b))
		if err != nil {
			log.Println("[error]", err)
		}
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
