package onion

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"go.uber.org/atomic"
)

var defaultConcurrency = 8

type Result struct {
	Url        string
	StatusCode int
	Headers    map[string][]string
	ErrorBody  string
}

type Results struct {
	KuboGWResult  *Result
	LassieResult  *Result
	L1ShimResult  *Result
	L1NginxResult *Result
}

type RequestExecutor struct {
	dir  string
	n    int
	wg   sync.WaitGroup
	reqs map[string]URLsToTest

	client *http.Client

	mu      sync.Mutex
	results map[string]*Results
}

func NewRequestExecutor(reqs map[string]URLsToTest, n int, dir string) *RequestExecutor {
	client := &http.Client{
		Transport: &http.Transport{
			MaxConnsPerHost:     1000,
			MaxIdleConnsPerHost: 1000,
			MaxIdleConns:        1000,
			IdleConnTimeout:     5 * time.Minute,
			TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
		},
		Timeout: 3 * time.Minute,
	}

	return &RequestExecutor{
		dir:     dir,
		n:       n,
		reqs:    reqs,
		results: make(map[string]*Results),
		client:  client,
	}
}

func (re *RequestExecutor) Execute() {
	fmt.Printf("\n --------------- Running round %d -------------------------------", re.n)
	fmt.Printf("\n Run-%d; Request Executor will execute requests for  %d  unique paths", re.n, len(re.reqs))
	re.wg.Add(4)
	go re.executeKuboRequests()
	go re.executeLassieRequests()
	go re.executeL1ShimRequests()
	go re.executeL1NginxRequests()
	re.wg.Wait()
	fmt.Printf("\n  Run-%d; Request Executor is Done", re.n)
}

func (re *RequestExecutor) executeLassieRequests() {
	defer re.wg.Done()

	ce := concurrentExecution{
		sem:   make(chan struct{}, defaultConcurrency),
		re:    re,
		count: atomic.NewInt32(0),
	}

	for _, o := range re.reqs {
		ce.do(o.Path, o.Lassie, "lassie")
	}

	ce.wg.Wait()
}

func (re *RequestExecutor) executeKuboRequests() {
	defer re.wg.Done()
	ce := concurrentExecution{
		sem:   make(chan struct{}, defaultConcurrency),
		re:    re,
		count: atomic.NewInt32(0),
	}

	for _, o := range re.reqs {
		ce.do(o.Path, o.KuboGWUrl, "kubogw")
	}

	ce.wg.Wait()
}

func (re *RequestExecutor) executeL1ShimRequests() {
	defer re.wg.Done()
	ce := concurrentExecution{
		sem:   make(chan struct{}, defaultConcurrency),
		re:    re,
		count: atomic.NewInt32(0),
	}

	for _, o := range re.reqs {
		ce.do(o.Path, o.L1Shim, "l1shim")
	}

	ce.wg.Wait()
}

func (re *RequestExecutor) executeL1NginxRequests() {
	defer re.wg.Done()

	ce := concurrentExecution{
		sem:   make(chan struct{}, defaultConcurrency),
		re:    re,
		count: atomic.NewInt32(0),
	}

	for _, o := range re.reqs {
		ce.do(o.Path, o.L1Nginx, "l1nginx")
	}

	ce.wg.Wait()
}

type concurrentExecution struct {
	wg    sync.WaitGroup
	sem   chan struct{}
	re    *RequestExecutor
	count *atomic.Int32
}

func (c *concurrentExecution) do(path, url, name string) {
	c.sem <- struct{}{}
	c.wg.Add(1)

	go func() {
		defer func() {
			<-c.sem
			c.wg.Done()
			fmt.Printf("\n Run-%d; Finished %d requests for %s", c.re.n, c.count.Inc(), name)
		}()
		result := c.re.executeHTTPRequest(url)
		c.re.recordResult(path, name, result)
	}()
}

func (re *RequestExecutor) executeHTTPRequest(url string) (result Result) {
	result = Result{
		Url: url,
	}

	resp, err := re.client.Get(url)
	if err != nil {
		result.ErrorBody = fmt.Sprintf("error sending request: %s", err.Error())
		return
	}
	defer resp.Body.Close()
	defer io.Copy(io.Discard, resp.Body)

	result.Headers = resp.Header
	result.StatusCode = resp.StatusCode

	if resp.StatusCode != http.StatusOK {
		errBody, err := io.ReadAll(resp.Body)
		if err != nil {
			result.ErrorBody = fmt.Sprintf("error reading response body: %s", err.Error())
			return
		}
		result.ErrorBody = string(errBody)
	}
	return
}

func (re *RequestExecutor) recordResult(path string, component string, result Result) {
	re.mu.Lock()
	defer re.mu.Unlock()

	_, ok := re.results[path]
	if !ok {
		re.results[path] = &Results{}
	}
	r := re.results[path]
	switch component {
	case "lassie":
		r.LassieResult = &result
	case "l1shim":
		r.L1ShimResult = &result
	case "kubogw":
		r.KuboGWResult = &result
	case "l1nginx":
		r.L1NginxResult = &result
	}
}

func (re *RequestExecutor) WriteResultsToFile() {
	re.mu.Lock()
	defer re.mu.Unlock()

	// marshal the results to json
	jsonData, err := json.MarshalIndent(re.results, "", " ")
	if err != nil {
		panic(err)
	}

	// create a new file and write the json data to it
	file, err := os.Create(fmt.Sprintf("%s/results.json", re.dir))
	if err != nil {
		panic(err)
	}
	defer file.Close()

	_, err = file.Write(jsonData)
	if err != nil {
		panic(err)
	}
}

func (re *RequestExecutor) WriteMismatchesToFile() {
	re.mu.Lock()
	defer re.mu.Unlock()

	writeMismatchF := func(mismatch map[string]Results, filename string) {
		jsonData, err := json.MarshalIndent(mismatch, "", " ")
		if err != nil {
			panic(err)
		}

		// create a new file and write the json data to it
		file, err := os.Create(filename)
		if err != nil {
			panic(err)
		}
		defer file.Close()

		_, err = file.Write(jsonData)
		if err != nil {
			panic(err)
		}
	}

	res := re.results

	kuboLassieMismatch := make(map[string]Results)
	lassiShimMismatch := make(map[string]Results)
	shimNginxMismatch := make(map[string]Results)

	type Result2xx struct {
		kubo   int
		lassie int
		shim   int
		nginx  int
	}

	result2xx := Result2xx{}

	for path, results := range res {
		results := *results

		if results.KuboGWResult.StatusCode == http.StatusOK {
			result2xx.kubo++
		}

		if results.LassieResult.StatusCode == http.StatusOK {
			result2xx.lassie++
		}

		if results.L1ShimResult.StatusCode == http.StatusOK {
			result2xx.shim++
		}

		if results.L1NginxResult.StatusCode == http.StatusOK {
			result2xx.nginx++
		}

		if results.KuboGWResult.StatusCode == http.StatusOK && results.LassieResult.StatusCode != http.StatusOK {
			rm := Results{}
			rm.KuboGWResult = results.KuboGWResult
			rm.LassieResult = results.LassieResult

			kuboLassieMismatch[path] = rm
		}

		if results.LassieResult.StatusCode == http.StatusOK && results.L1ShimResult.StatusCode != http.StatusOK {
			rm := Results{}
			rm.LassieResult = results.LassieResult
			rm.L1ShimResult = results.L1ShimResult
			lassiShimMismatch[path] = rm
		}

		if results.L1ShimResult.StatusCode == http.StatusOK && results.L1NginxResult.StatusCode != http.StatusOK {
			rm := Results{}
			rm.L1ShimResult = results.L1ShimResult
			rm.L1NginxResult = results.L1NginxResult
			shimNginxMismatch[path] = rm
		}
	}

	kl := fmt.Sprintf("%s/kubo-lassie-mismatch.json", re.dir)
	ls := fmt.Sprintf("%s/lassie-shim-mismatch.json", re.dir)
	sn := fmt.Sprintf("%s/shim-nginx-mismatch.json", re.dir)

	writeMismatchF(kuboLassieMismatch, kl)
	writeMismatchF(lassiShimMismatch, ls)
	writeMismatchF(shimNginxMismatch, sn)

	fmt.Printf("\n Run-%d; Total Unique Requests: %d", re.n, len(res))
	fmt.Printf("\n Run-%d; Total 2xx from Kubo GW: %d", re.n, result2xx.kubo)
	fmt.Printf("\n Run-%d; Total 2xx from Lassie: %d", re.n, result2xx.lassie)
	fmt.Printf("\n Run-%d; Total 2xx from L1 Shim: %d", re.n, result2xx.shim)
	fmt.Printf("\n Run-%d; Total 2xx from L1 Nginx: %d", re.n, result2xx.nginx)

	fmt.Printf("\n Run-%d; Kubo Lassie Mismatch: %d", re.n, len(kuboLassieMismatch))
	fmt.Printf("\n Run-%d; Lassie Shim Mismatch: %d", re.n, len(lassiShimMismatch))
	fmt.Printf("\n Run-%d; Shim Nginx Mismatch: %d", re.n, len(shimNginxMismatch))
}
