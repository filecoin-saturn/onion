package onion

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"go.uber.org/atomic"
)

var defaultConcurrency = 10

type ResponseBytesMismatch struct {
	LassieShimMismatches    map[string]Results
	LassieShimMismatchPaths []string

	ShimNginxMismatches    map[string]Results
	ShimNginxMismatchPaths []string

	TotalLassieReadSuccess  int
	TotalL1ShimReadSuccess  int
	TotalL1NginxReadSuccess int

	TotalLassieReadError  int
	TotalL1ShimReadError  int
	TotalL1NginxReadError int
}

type Result struct {
	Url        string
	StatusCode int
	Headers    map[string][]string
	ErrorBody  string

	ResponseBodyReadError string
	ResponseBody          []byte
	ResponseSize          uint64
}

type Results struct {
	KuboGWResult  *Result
	LassieResult  *Result
	L1ShimResult  *Result
	L1NginxResult *Result
}

type RequestExecutor struct {
	readResponse bool
	dir          string
	rrdir        string
	n            int
	reqs         map[string]URLsToTest

	client *http.Client

	mu            sync.Mutex
	results       map[string]*Results
	responseReads *ResponseBytesMismatch
}

func NewRequestExecutor(reqs map[string]URLsToTest, n int, dir string, rrdir string, rr bool) *RequestExecutor {
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
		readResponse: rr,
		dir:          dir,
		rrdir:        rrdir,
		n:            n,
		reqs:         reqs,
		results:      make(map[string]*Results),
		client:       client,
		responseReads: &ResponseBytesMismatch{
			LassieShimMismatches: make(map[string]Results),
			ShimNginxMismatches:  make(map[string]Results),
		},
	}
}

func (re *RequestExecutor) Execute() {
	fmt.Printf("\n --------------- Running round %d -------------------------------", re.n)
	fmt.Printf("\n Run-%d; Request Executor will execute requests for  %d  unique paths with rr flag %t", re.n, len(re.reqs), re.readResponse)

	sem := make(chan struct{}, defaultConcurrency)
	count := atomic.NewInt32(0)
	var wg sync.WaitGroup

	for _, req := range re.reqs {
		path := req.Path
		wg.Add(1)
		sem <- struct{}{}
		go func(path string) {
			defer func() {
				<-sem
				wg.Done()
			}()
			re.executeRequest(path, count.Inc())
		}(path)
	}
	wg.Wait()

	fmt.Printf("\n  Run-%d; Request Executor is Done", re.n)
}

func (re *RequestExecutor) executeRequest(path string, count int32) {
	fmt.Printf("\n  Run-%d; Request Executor is executing request %d", re.n, count)
	urls := re.reqs[path]

	var lassieRbs []byte
	var l1ShimRbs []byte
	var l1NginxRbs []byte

	addResultF := func(result Result, component string) {
		re.mu.Lock()
		defer re.mu.Unlock()
		_, ok := re.results[path]
		if !ok {
			re.results[path] = &Results{}
		}
		rs := re.results[path]

		switch component {
		case "lassie":
			rs.LassieResult = &result
		case "l1shim":
			rs.L1ShimResult = &result
		case "kubogw":
			rs.KuboGWResult = &result
		case "l1nginx":
			rs.L1NginxResult = &result
		}
	}

	var wg sync.WaitGroup
	wg.Add(3)

	// Kubo
	if !re.readResponse {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := re.executeHTTPRequest(urls.KuboGWUrl, true)
			addResultF(result, "kubogw")
			fmt.Printf("\n  Run-%d; Request Executor is done executing request %d for Kubo", re.n, count)
		}()
	}

	// Lassie
	go func() {
		defer wg.Done()
		result := re.executeHTTPRequest(urls.Lassie, false)
		lassieRbs = result.ResponseBody
		fmt.Printf("\n  Run-%d; Got %d bytes from Lassie for request %d", re.n, len(lassieRbs), count)

		result.ResponseBody = nil
		addResultF(result, "lassie")
		fmt.Printf("\n  Run-%d; Request Executor is done executing request %d for Lassie", re.n, count)
	}()

	// L1 Shim
	go func() {
		defer wg.Done()
		result := re.executeHTTPRequest(urls.L1Shim, false)

		fmt.Printf("\n  Run-%d; Got %d bytes from L1 Shim for request %d", re.n, len(result.ResponseBody), count)

		l1ShimRbs = result.ResponseBody
		result.ResponseBody = nil
		addResultF(result, "l1shim")
		fmt.Printf("\n  Run-%d; Request Executor is done executing request %d for L1-Shim", re.n, count)
	}()

	// L1 Nginx
	go func() {
		defer wg.Done()
		result := re.executeHTTPRequest(urls.L1Nginx, false)

		fmt.Printf("\n  Run-%d; Got %d bytes from L1 Nginx for request %d", re.n, len(result.ResponseBody), count)

		l1NginxRbs = result.ResponseBody
		result.ResponseBody = nil
		addResultF(result, "l1nginx")
		fmt.Printf("\n  Run-%d; Request Executor is done executing request %d for L1 Nginx", re.n, count)
	}()

	wg.Wait()
	fmt.Printf("\n  Run-%d; Request Executor is done executing overall request %d", re.n, count)

	if re.readResponse {
		re.mu.Lock()
		defer re.mu.Unlock()

		rs := re.results[path]
		rbm := re.responseReads

		// lassie response read ok ?
		if rs.LassieResult.StatusCode == http.StatusOK {
			if len(rs.LassieResult.ResponseBodyReadError) == 0 {
				rbm.TotalLassieReadSuccess++
			} else {
				rbm.TotalLassieReadError++
			}
		}
		responseCodeMetric.WithLabelValues("lassie", strconv.Itoa(rs.LassieResult.StatusCode)).Inc()

		if rs.L1ShimResult.StatusCode == http.StatusOK {
			if len(rs.L1ShimResult.ResponseBodyReadError) == 0 {
				rbm.TotalL1ShimReadSuccess++
			} else {
				rbm.TotalL1ShimReadError++
			}
		}
		responseCodeMetric.WithLabelValues("shim", strconv.Itoa(rs.L1ShimResult.StatusCode)).Inc()

		if rs.L1NginxResult.StatusCode == http.StatusOK {
			if len(rs.L1NginxResult.ResponseBodyReadError) == 0 {
				rbm.TotalL1NginxReadSuccess++
			} else {
				rbm.TotalL1NginxReadError++
			}
		}
		responseCodeMetric.WithLabelValues("nginx", strconv.Itoa(rs.L1NginxResult.StatusCode)).Inc()

		//  discrepancies
		// if both are 200 and both were able to give responses -> compare bytes
		if rs.LassieResult.StatusCode == http.StatusOK && rs.L1ShimResult.StatusCode == http.StatusOK &&
			len(rs.LassieResult.ResponseBodyReadError) == 0 && len(rs.L1ShimResult.ResponseBodyReadError) == 0 {
			if !bytes.Equal(lassieRbs, l1ShimRbs) {
				rm := Results{}
				rm.LassieResult = rs.LassieResult
				rm.L1ShimResult = rs.L1ShimResult
				rbm.LassieShimMismatches[path] = rm
				rbm.LassieShimMismatchPaths = append(rbm.LassieShimMismatchPaths, path)

				responseSizeMismatchMetric.WithLabelValues("lassie-shim").Inc()
			}
		}

		// if both are 200 and both were able to give responses -> compare bytes
		if rs.L1ShimResult.StatusCode == http.StatusOK && rs.L1NginxResult.StatusCode == http.StatusOK &&
			len(rs.L1ShimResult.ResponseBodyReadError) == 0 && len(rs.L1NginxResult.ResponseBodyReadError) == 0 {
			if !bytes.Equal(l1ShimRbs, l1NginxRbs) {
				rm := Results{}
				rm.L1ShimResult = rs.L1ShimResult
				rm.L1NginxResult = rs.L1NginxResult
				rbm.ShimNginxMismatches[path] = rm
				rbm.ShimNginxMismatchPaths = append(rbm.ShimNginxMismatchPaths, path)

				responseSizeMismatchMetric.WithLabelValues("shim-nginx").Inc()
			}
		}
	}
}

func (re *RequestExecutor) executeHTTPRequest(url string, isKuboGW bool) (result Result) {
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

	if !isKuboGW && re.readResponse && resp.StatusCode == http.StatusOK {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			result.ResponseBodyReadError = fmt.Sprintf("error reading response body: %s", err.Error())
			return
		}
		result.ResponseBody = body
		result.ResponseSize = uint64(len(body))
	}

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

	writePathsF := func(mismatch []string, filename string) {
		jsonData, err := json.MarshalIndent(mismatch, "", " ")
		if err != nil {
			panic(err)
		}

		if err := os.WriteFile(filename, jsonData, 0755); err != nil {
			panic(err)
		}
	}

	res := re.results

	kuboLassieMismatch := make(map[string]Results)
	lassiShimMismatch := make(map[string]Results)
	shimNginxMismatch := make(map[string]Results)

	var klMismatchPaths []string
	var lsMismatchPaths []string
	var snMismatchPaths []string

	type Result2xx struct {
		kubo   int
		lassie int
		shim   int
		nginx  int
	}

	result2xx := Result2xx{}

	for path, results := range res {
		results := *results

		if !re.readResponse {
			if results.KuboGWResult.StatusCode == http.StatusOK {
				result2xx.kubo++
			}
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

		if !re.readResponse {
			if results.KuboGWResult.StatusCode == http.StatusOK && results.LassieResult.StatusCode != http.StatusOK {
				rm := Results{}
				rm.KuboGWResult = results.KuboGWResult
				rm.LassieResult = results.LassieResult

				kuboLassieMismatch[path] = rm
				klMismatchPaths = append(klMismatchPaths, path)

				responseCodeMismatchMetric.WithLabelValues("kubo-lassie").Inc()
			}
		}

		if results.LassieResult.StatusCode == http.StatusOK && results.L1ShimResult.StatusCode != http.StatusOK {
			rm := Results{}
			rm.LassieResult = results.LassieResult
			rm.L1ShimResult = results.L1ShimResult
			lassiShimMismatch[path] = rm
			lsMismatchPaths = append(lsMismatchPaths, path)

			responseCodeMismatchMetric.WithLabelValues("lassie-shim").Inc()
		}

		if results.L1ShimResult.StatusCode == http.StatusOK && results.L1NginxResult.StatusCode != http.StatusOK {
			rm := Results{}
			rm.L1ShimResult = results.L1ShimResult
			rm.L1NginxResult = results.L1NginxResult
			shimNginxMismatch[path] = rm
			snMismatchPaths = append(snMismatchPaths, path)

			responseCodeMismatchMetric.WithLabelValues("shim-nginx").Inc()
		}
	}

	kl := fmt.Sprintf("%s/kubo-lassie-mismatch.json", re.dir)
	ls := fmt.Sprintf("%s/lassie-shim-mismatch.json", re.dir)
	sn := fmt.Sprintf("%s/shim-nginx-mismatch.json", re.dir)

	writeMismatchF(kuboLassieMismatch, kl)
	writeMismatchF(lassiShimMismatch, ls)
	writeMismatchF(shimNginxMismatch, sn)

	if re.readResponse {
		bzs, rerr := json.MarshalIndent(re.responseReads, "", " ")
		if rerr != nil {
			panic(rerr)
		}
		if err := os.WriteFile(fmt.Sprintf("%s/response-reads.json", re.rrdir), bzs, 0755); err != nil {
			panic(err)
		}

		bz, err := json.MarshalIndent(re.responseReads.LassieShimMismatchPaths, "", " ")
		if err != nil {
			panic(err)
		}
		if err := os.WriteFile(fmt.Sprintf("%s/lassie-shim-mismatch-paths.json", re.rrdir), bz, 0755); err != nil {
			panic(err)
		}

		bz, err = json.MarshalIndent(re.responseReads.ShimNginxMismatchPaths, "", " ")
		if err != nil {
			panic(err)
		}
		if err := os.WriteFile(fmt.Sprintf("%s/shim-nginx-mismatch-paths.json", re.rrdir), bz, 0755); err != nil {
			panic(err)
		}

		bz, err = json.MarshalIndent(re.responseReads.LassieShimMismatches, "", " ")
		if err != nil {
			panic(err)
		}
		if err := os.WriteFile(fmt.Sprintf("%s/lassie-shim-mismatches.json", re.rrdir), bz, 0755); err != nil {
			panic(err)
		}

		bz, err = json.MarshalIndent(re.responseReads.ShimNginxMismatches, "", " ")
		if err != nil {
			panic(err)
		}
		if err := os.WriteFile(fmt.Sprintf("%s/shim-nginx-mismatches.json", re.rrdir), bz, 0755); err != nil {
			panic(err)
		}
	}

	writePathsF(klMismatchPaths, fmt.Sprintf("%s/kubo-lassie-mismatch-paths.json", re.dir))
	writePathsF(lsMismatchPaths, fmt.Sprintf("%s/lassie-shim-mismatch-paths.json", re.dir))
	writePathsF(snMismatchPaths, fmt.Sprintf("%s/shim-nginx-mismatch-paths.json", re.dir))

	fmt.Printf("\n Run-%d; Total Unique Requests: %d", re.n, len(res))
	if !re.readResponse {
		fmt.Printf("\n Run-%d; Total 2xx from Kubo GW: %d", re.n, result2xx.kubo)
	}
	fmt.Printf("\n Run-%d; Total 2xx from Lassie: %d", re.n, result2xx.lassie)
	fmt.Printf("\n Run-%d; Total 2xx from L1 Shim: %d", re.n, result2xx.shim)
	fmt.Printf("\n Run-%d; Total 2xx from L1 Nginx: %d", re.n, result2xx.nginx)

	if !re.readResponse {
		fmt.Printf("\n Run-%d; Kubo Lassie 2XX Mismatch: %d", re.n, len(kuboLassieMismatch))
	}
	fmt.Printf("\n Run-%d; Lassie Shim 2XX Mismatch: %d", re.n, len(lassiShimMismatch))
	fmt.Printf("\n Run-%d; Shim Nginx 2XX Mismatch: %d\n", re.n, len(shimNginxMismatch))

	if re.readResponse {
		fmt.Printf("\n Run-%d; Lassie Shim response size Mismatch: %d\n", re.n, len(re.responseReads.LassieShimMismatchPaths))
		fmt.Printf("\n Run-%d; Shim Nginx response size Mismatch: %d\n", re.n, len(re.responseReads.ShimNginxMismatchPaths))
	}

	toplLevel := struct {
		Kubo2XX   int
		Lassie2XX int
		Shim2XX   int
		Nginx2XX  int

		KuboLassieMismatch int
		LassieShimMismatch int
		ShimNginxMismatch  int
	}{
		Kubo2XX:            result2xx.kubo,
		Lassie2XX:          result2xx.lassie,
		Shim2XX:            result2xx.shim,
		Nginx2XX:           result2xx.nginx,
		KuboLassieMismatch: len(kuboLassieMismatch),
		LassieShimMismatch: len(lassiShimMismatch),
		ShimNginxMismatch:  len(shimNginxMismatch),
	}
	bz, err := json.MarshalIndent(toplLevel, "", " ")
	if err != nil {
		panic(err)
	}

	if err := os.WriteFile(fmt.Sprintf("%s/top-level-metrics.json", re.dir), bz, 0755); err != nil {
		panic(err)
	}

	// write metrics
	for _, m := range metrics {
		if err := pushMetric(re.n, m); err != nil {
			panic(err)
		}
	}

	// write mismatched paths separately
}
