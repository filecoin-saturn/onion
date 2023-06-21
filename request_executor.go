package onion

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"go.uber.org/atomic"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

var defaultConcurrency = 5

type ResponseBytesMismatch struct {
	KuboLassieMismatches    map[string]Results
	KuboLassieMismatchPaths []string

	KuboL1ShimMismatches    map[string]Results
	KuboL1ShimMismatchPaths []string

	KuboL1NginxMismatches    map[string]Results
	KuboL1NginxMismatchPaths []string

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

	LassieReadErrors  map[string]*Result
	L1ShimReadErrors  map[string]*Result
	L1NginxReadErrors map[string]*Result

	LassieReadErrorPaths  []string
	L1ShimReadErrorPaths  []string
	L1NginxReadErrorPaths []string

	TotalKuboLassieMatches  int
	TotalKuboL1ShimMatches  int
	TotalKuboL1NginxMatches int
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
	dir   string
	rrdir string
	n     int
	reqs  map[string]URLsToTest

	client *http.Client

	mu            sync.Mutex
	results       map[string]*Results
	responseReads *ResponseBytesMismatch
}

func NewRequestExecutor(reqs map[string]URLsToTest, n int, dir string, rrdir string) *RequestExecutor {
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
		rrdir:   rrdir,
		n:       n,
		reqs:    reqs,
		results: make(map[string]*Results),
		client:  client,
		responseReads: &ResponseBytesMismatch{
			KuboLassieMismatches: make(map[string]Results),
			LassieShimMismatches: make(map[string]Results),
			ShimNginxMismatches:  make(map[string]Results),

			LassieReadErrors:  make(map[string]*Result),
			L1ShimReadErrors:  make(map[string]*Result),
			L1NginxReadErrors: make(map[string]*Result),

			KuboL1ShimMismatches:  make(map[string]Results),
			KuboL1NginxMismatches: make(map[string]Results),
		},
	}
}

func (re *RequestExecutor) Execute() {
	fmt.Printf("\n --------------- Running round %d -------------------------------", re.n)
	fmt.Printf("\n Run-%d; Request Executor will execute requests for  %d  unique paths", re.n, len(re.reqs))

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
	var kuboGWRbs []byte

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
	wg.Add(4)

	// Kubo

	go func() {
		defer wg.Done()
		result := re.executeHTTPRequest(urls.KuboGWUrl, true)
		kuboGWRbs = result.ResponseBody
		result.ResponseBody = nil
		addResultF(result, "kubogw")
		fmt.Printf("\n  Run-%d; Request Executor is done executing request %d for Kubo", re.n, count)
	}()

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

	re.mu.Lock()
	defer re.mu.Unlock()

	rs := re.results[path]
	rbm := re.responseReads

	// lassie response read ok ?
	if rs.LassieResult.StatusCode == http.StatusOK {
		if len(rs.LassieResult.ResponseBodyReadError) == 0 {
			rbm.TotalLassieReadSuccess++
		} else {
			rbm.LassieReadErrors[path] = rs.LassieResult
			rbm.LassieReadErrorPaths = append(rbm.LassieReadErrorPaths, path)
			rbm.TotalLassieReadError++
		}
	}

	if rs.L1ShimResult.StatusCode == http.StatusOK {
		if len(rs.L1ShimResult.ResponseBodyReadError) == 0 {
			rbm.TotalL1ShimReadSuccess++
		} else {
			rbm.L1ShimReadErrors[path] = rs.L1ShimResult
			rbm.L1ShimReadErrorPaths = append(rbm.L1ShimReadErrorPaths, path)
			rbm.TotalL1ShimReadError++
		}
	}

	if rs.L1NginxResult.StatusCode == http.StatusOK {
		if len(rs.L1NginxResult.ResponseBodyReadError) == 0 {
			rbm.TotalL1NginxReadSuccess++
		} else {
			rbm.L1NginxReadErrors[path] = rs.L1NginxResult
			rbm.L1NginxReadErrorPaths = append(rbm.L1NginxReadErrorPaths, path)
			rbm.TotalL1NginxReadError++
		}
	}

	//  discrepancies
	// if both are 200 and both were able to give responses -> compare bytes
	if rs.KuboGWResult.StatusCode == http.StatusOK && rs.LassieResult.StatusCode == http.StatusOK &&
		len(rs.KuboGWResult.ResponseBodyReadError) == 0 && len(rs.LassieResult.ResponseBodyReadError) == 0 {
		raw, err := ExtractRaw(lassieRbs)
		if err == nil {
			if !bytes.Equal(kuboGWRbs, raw) {
				rm := Results{}
				rm.KuboGWResult = rs.KuboGWResult
				rm.LassieResult = rs.LassieResult
				rbm.KuboLassieMismatches[path] = rm
				rbm.KuboLassieMismatchPaths = append(rbm.KuboLassieMismatchPaths, path)
			} else {
				rbm.TotalKuboLassieMatches++
			}
		} else {
			fmt.Printf("\n  Run-%d; Error extracting raw bytes from Lassie response for request %d; err=%s, path=%s", re.n, count, err, path)
			if strings.Contains(err.Error(), "could not find") {
				if err := os.WriteFile(fmt.Sprintf("lassie-%s.car", ParseCidFromPath(path)), lassieRbs, 0755); err != nil {
					panic(err)
				}
			}
		}
	}

	if rs.KuboGWResult.StatusCode == http.StatusOK && rs.L1ShimResult.StatusCode == http.StatusOK &&
		len(rs.KuboGWResult.ResponseBodyReadError) == 0 && len(rs.L1ShimResult.ResponseBodyReadError) == 0 {
		raw, err := ExtractRaw(l1ShimRbs)
		if err == nil {
			if !bytes.Equal(kuboGWRbs, raw) {
				rm := Results{}
				rm.KuboGWResult = rs.KuboGWResult
				rm.L1ShimResult = rs.L1ShimResult
				rbm.KuboL1ShimMismatches[path] = rm
				rbm.KuboL1ShimMismatchPaths = append(rbm.KuboL1ShimMismatchPaths, path)
			} else {
				rbm.TotalKuboL1ShimMatches++
			}
		} else {
			fmt.Printf("\n  Run-%d; Error extracting raw bytes from L1 Shim response for request %d; err=%s", re.n, count, err)
		}
	}

	if rs.KuboGWResult.StatusCode == http.StatusOK && rs.L1NginxResult.StatusCode == http.StatusOK &&
		len(rs.KuboGWResult.ResponseBodyReadError) == 0 && len(rs.L1NginxResult.ResponseBodyReadError) == 0 {
		raw, err := ExtractRaw(l1NginxRbs)
		if err == nil {
			if !bytes.Equal(kuboGWRbs, raw) {
				rm := Results{}
				rm.KuboGWResult = rs.KuboGWResult
				rm.L1NginxResult = rs.L1NginxResult
				rbm.KuboL1NginxMismatches[path] = rm
				rbm.KuboL1NginxMismatchPaths = append(rbm.KuboL1NginxMismatchPaths, path)
			} else {
				rbm.TotalKuboL1NginxMatches++
			}
		} else {
			fmt.Printf("\n  Run-%d; Error extracting raw bytes from L1 Nginx response for request %d; err=%s", re.n, count, err)
		}
	}

	if rs.LassieResult.StatusCode == http.StatusOK && rs.L1ShimResult.StatusCode == http.StatusOK &&
		len(rs.LassieResult.ResponseBodyReadError) == 0 && len(rs.L1ShimResult.ResponseBodyReadError) == 0 {
		if !bytes.Equal(lassieRbs, l1ShimRbs) {
			rm := Results{}
			rm.LassieResult = rs.LassieResult
			rm.L1ShimResult = rs.L1ShimResult
			rbm.LassieShimMismatches[path] = rm
			rbm.LassieShimMismatchPaths = append(rbm.LassieShimMismatchPaths, path)
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

	if resp.StatusCode == http.StatusOK {
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

		if results.KuboGWResult.StatusCode == http.StatusOK && len(results.KuboGWResult.ResponseBodyReadError) == 0 {
			result2xx.kubo++
			if results.LassieResult.StatusCode != http.StatusOK || len(results.LassieResult.ResponseBodyReadError) != 0 {
				rm := Results{}
				rm.KuboGWResult = results.KuboGWResult
				rm.LassieResult = results.LassieResult
				kuboLassieMismatch[path] = rm
				klMismatchPaths = append(klMismatchPaths, path)
			}
		}

		if results.LassieResult.StatusCode == http.StatusOK && len(results.LassieResult.ResponseBodyReadError) == 0 {
			result2xx.lassie++
			if results.L1ShimResult.StatusCode != http.StatusOK || len(results.L1ShimResult.ResponseBodyReadError) != 0 {
				rm := Results{}
				rm.LassieResult = results.LassieResult
				rm.L1ShimResult = results.L1ShimResult
				lassiShimMismatch[path] = rm
				lsMismatchPaths = append(lsMismatchPaths, path)
			}
		}

		if results.L1ShimResult.StatusCode == http.StatusOK && len(results.L1ShimResult.ResponseBodyReadError) == 0 {
			result2xx.shim++
			if results.L1NginxResult.StatusCode != http.StatusOK || len(results.L1NginxResult.ResponseBodyReadError) != 0 {
				rm := Results{}
				rm.L1ShimResult = results.L1ShimResult
				rm.L1NginxResult = results.L1NginxResult
				shimNginxMismatch[path] = rm
				snMismatchPaths = append(snMismatchPaths, path)
			}
		}

		if results.L1NginxResult.StatusCode == http.StatusOK && len(results.L1NginxResult.ResponseBodyReadError) == 0 {
			result2xx.nginx++
		}
	}

	kl := fmt.Sprintf("%s/kubo-lassie-mismatch.json", re.dir)
	ls := fmt.Sprintf("%s/lassie-shim-mismatch.json", re.dir)
	sn := fmt.Sprintf("%s/shim-nginx-mismatch.json", re.dir)

	writeMismatchF(kuboLassieMismatch, kl)
	writeMismatchF(lassiShimMismatch, ls)
	writeMismatchF(shimNginxMismatch, sn)

	bzs, rerr := json.MarshalIndent(re.responseReads, "", " ")
	if rerr != nil {
		panic(rerr)
	}
	if err := os.WriteFile(fmt.Sprintf("%s/response-reads.json", re.rrdir), bzs, 0755); err != nil {
		panic(err)
	}

	bz, err := json.MarshalIndent(re.responseReads.KuboLassieMismatchPaths, "", " ")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(fmt.Sprintf("%s/kubo-lassie-mismatch-paths.json", re.rrdir), bz, 0755); err != nil {
		panic(err)
	}

	bz, err = json.MarshalIndent(re.responseReads.KuboLassieMismatches, "", " ")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(fmt.Sprintf("%s/kubo-lassie-mismatches.json", re.rrdir), bz, 0755); err != nil {
		panic(err)
	}

	bz, err = json.MarshalIndent(re.responseReads.LassieShimMismatchPaths, "", " ")
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

	bz, err = json.MarshalIndent(re.responseReads.LassieReadErrorPaths, "", " ")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(fmt.Sprintf("%s/lassie-2xx-response-read-error-paths.json", re.rrdir), bz, 0755); err != nil {
		panic(err)
	}

	bz, err = json.MarshalIndent(re.responseReads.L1ShimReadErrorPaths, "", " ")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(fmt.Sprintf("%s/shim-2xx-response-read-error-paths.json", re.rrdir), bz, 0755); err != nil {
		panic(err)
	}

	bz, err = json.MarshalIndent(re.responseReads.L1NginxReadErrorPaths, "", " ")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(fmt.Sprintf("%s/nginx-2xx-response-read-error-paths.json", re.rrdir), bz, 0755); err != nil {
		panic(err)
	}

	bz, err = json.MarshalIndent(re.responseReads.LassieReadErrors, "", " ")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(fmt.Sprintf("%s/lassie-2xx-response-read-errors.json", re.rrdir), bz, 0755); err != nil {
		panic(err)
	}

	bz, err = json.MarshalIndent(re.responseReads.L1ShimReadErrors, "", " ")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(fmt.Sprintf("%s/shim-2xx-response-read-errors.json", re.rrdir), bz, 0755); err != nil {
		panic(err)
	}
	bz, err = json.MarshalIndent(re.responseReads.L1NginxReadErrors, "", " ")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(fmt.Sprintf("%s/nginx-2xx-response-read-errors.json", re.rrdir), bz, 0755); err != nil {
		panic(err)
	}

	writePathsF(klMismatchPaths, fmt.Sprintf("%s/kubo-lassie-mismatch-paths.json", re.dir))
	writePathsF(lsMismatchPaths, fmt.Sprintf("%s/lassie-shim-mismatch-paths.json", re.dir))
	writePathsF(snMismatchPaths, fmt.Sprintf("%s/shim-nginx-mismatch-paths.json", re.dir))

	fmt.Println("\n ------SUMMARY OF SUCCESS------------------")
	fmt.Printf("\n Run-%d; Total Unique Requests: %d", re.n, len(res))
	fmt.Printf("\n Run-%d; Total 2xx with successful response reads from Kubo GW: %d", re.n, result2xx.kubo)
	fmt.Printf("\n Run-%d; Total 2xx with successful response reads from Lassie: %d", re.n, result2xx.lassie)
	fmt.Printf("\n Run-%d; Total 2xx with successful response reads from L1 Shim: %d", re.n, result2xx.shim)
	fmt.Printf("\n Run-%d; Total 2xx with successful response reads from L1 Nginx: %d", re.n, result2xx.nginx)
	fmt.Println("\n ------------------------")

	fmt.Printf("\n Run-%d; Kubo <> Lassie (2xx + successful response read) Mismatch: %d", re.n, len(kuboLassieMismatch))
	c := NewCidContactChecker(klMismatchPaths)
	c.Check()
	fmt.Printf("\n Run-%d; Lassie <> Shim (2xx + successful response read) Mismatch: %d", re.n, len(lassiShimMismatch))
	c = NewCidContactChecker(lsMismatchPaths)
	c.Check()
	fmt.Printf("\n Run-%d; Shim <> Nginx (2xx + successful response read) Mismatch: %d\n", re.n, len(shimNginxMismatch))
	c = NewCidContactChecker(snMismatchPaths)
	c.Check()
	fmt.Println("\n----")

	fmt.Println("\n ----------SUMMARY OF RESPONSE BYTES MISMATCHES --------------")
	fmt.Printf("\n Run-%d; Kubo Lassie response bytes Mismatch(after extracting Lassie CAR): %d", re.n, len(re.responseReads.KuboLassieMismatchPaths))
	fmt.Printf("\n Run-%d; Kubo Shim response bytes Mismatch(after extracting Shim CAR): %d", re.n, len(re.responseReads.KuboL1ShimMismatches))
	fmt.Printf("\n Run-%d; Kubo Nginx response bytes Mismatch(after extracting Nginx CAR): %d", re.n, len(re.responseReads.KuboL1NginxMismatches))
	fmt.Printf("\n Run-%d; Lassie Shim response bytes Mismatch: %d", re.n, len(re.responseReads.LassieShimMismatchPaths))
	fmt.Printf("\n Run-%d; Shim Nginx response bytes Mismatch: %d", re.n, len(re.responseReads.ShimNginxMismatchPaths))

	/*fmt.Println("\n ----------SUMMARY OF RESPONSE READ ERRORS --------------")
	fmt.Printf("\n Run-%d; Lassie returned 200 but failed to read responses for %d requests", re.n, re.responseReads.TotalLassieReadError)
	c = NewCidContactChecker(re.responseReads.LassieReadErrorPaths)
	c.Check()
	fmt.Println("\n----")

	fmt.Printf("\n Run-%d; Shim returned 200 but failed to read responses for %d requests", re.n, re.responseReads.TotalL1ShimReadError)
	c = NewCidContactChecker(re.responseReads.L1ShimReadErrorPaths)
	c.Check()
	fmt.Println("\n----")

	fmt.Printf("\n Run-%d; Nginx returned 200 but failed to read responses for %d requests", re.n, re.responseReads.TotalL1NginxReadError)
	c = NewCidContactChecker(re.responseReads.L1NginxReadErrorPaths)
	c.Check()
	fmt.Println("\n----")*/

	fmt.Println("\n ----------DONE; Please see the results/ directory for detailed request logs --------------")

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
	bz, err = json.MarshalIndent(toplLevel, "", " ")
	if err != nil {
		panic(err)
	}

	if err := os.WriteFile(fmt.Sprintf("%s/top-level-metrics.json", re.dir), bz, 0755); err != nil {
		panic(err)
	}

	// write mismatched paths separately
}
