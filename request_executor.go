package onion

import (
	"encoding/json"
	"fmt"
	"go.uber.org/atomic"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

var defaultConcurrency = 5

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
	wg   sync.WaitGroup
	reqs map[string]URLsToTest

	client *http.Client

	mu      sync.Mutex
	results map[string]*Results
}

func NewRequestExecutor(reqs map[string]URLsToTest) *RequestExecutor {
	client := &http.Client{
		Transport: &http.Transport{
			MaxConnsPerHost:     1000,
			MaxIdleConnsPerHost: 1000,
			MaxIdleConns:        1000,
			IdleConnTimeout:     5 * time.Minute,
		},
		Timeout: 3 * time.Minute,
	}

	return &RequestExecutor{
		reqs:    reqs,
		results: make(map[string]*Results),
		client:  client,
	}
}

func (re *RequestExecutor) Execute() {
	fmt.Printf("\n Request Executor will execute requests for  %d  unique paths", len(re.reqs))
	re.wg.Add(4)
	go re.executeKuboRequests()
	go re.executeLassieRequests()
	go re.executeL1ShimRequests()
	go re.executeL1NginxRequests()
	re.wg.Wait()
	fmt.Println("\n Request Executor is Done")
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
			fmt.Printf("\n Finished %d requests for %s", c.count.Inc(), name)
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

func (re *RequestExecutor) WriteResultsToFile(filename string) {
	re.mu.Lock()
	defer re.mu.Unlock()

	// marshal the results to json
	jsonData, err := json.MarshalIndent(re.results, "", " ")
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

	writeMismatchF(kuboLassieMismatch, "results/kubo-lassie-mismatch.json")
	writeMismatchF(lassiShimMismatch, "results/lassie-shim-mismatch.json")
	writeMismatchF(shimNginxMismatch, "results/shim-nginx-mismatch.json")

	fmt.Printf("\n Total Unique Requests: %d", len(res))
	fmt.Printf("\n Total 2xx from Kubo GW: %d", result2xx.kubo)
	fmt.Printf("\n Total 2xx from Lassie: %d", result2xx.lassie)
	fmt.Printf("\n Total 2xx from L1 Shim: %d", result2xx.shim)
	fmt.Printf("\n Total 2xx from L1 Nginx: %d", result2xx.nginx)

	fmt.Printf("\n Kubo Lassie Mismatch: %d", len(kuboLassieMismatch))
	fmt.Printf("\n Lassie Shim Mismatch: %d", len(lassiShimMismatch))
	fmt.Printf("\n Shim Nginx Mismatch: %d", len(shimNginxMismatch))

}
