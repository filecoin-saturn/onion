package onion

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var cidContactUrl = "https://cid.contact/cid/%s"

type CidContactChecker struct {
	client     http.Client
	mismatches []string
}

type CidContactOutput struct {
	Status     int
	Response   string
	IsDagHouse bool
	IsPinata   bool
}

func NewCidContactChecker(mismatches []string) *CidContactChecker {
	return &CidContactChecker{
		client: http.Client{
			Transport: &http.Transport{
				MaxConnsPerHost:     1000,
				MaxIdleConnsPerHost: 1000,
				MaxIdleConns:        1000,
			},
			Timeout: 3 * time.Minute,
		},
		mismatches: mismatches,
	}
}

func (klm *CidContactChecker) Check() {
	type Summary struct {
		NotFoundOnCidContact int
		DAGHouseCid          int
		PinataCid            int
		Others               int
	}

	sum := Summary{}

	for _, path := range klm.mismatches {
		// check cid.contact
		cid := ParseCidFromPath(path)
		var cc *CidContactOutput
		var err error

		for {
			cc, err = klm.GetCidContactResponse(cid)
			if err != nil {
				time.Sleep(1 * time.Second)
				continue
			}
			break
		}

		if cc.Status == http.StatusNotFound {
			sum.NotFoundOnCidContact++
		} else if cc.IsDagHouse {
			sum.DAGHouseCid++
		} else if cc.IsPinata {
			sum.PinataCid++
		} else {
			sum.Others++
		}
	}

	fmt.Println("\n--- cid.contact Summary of mismatches---")
	bz, err := json.MarshalIndent(sum, "", " ")
	if err != nil {
		panic(err)
	}
	fmt.Println(string(bz))
}

func (klm *CidContactChecker) GetCidContactResponse(cid string) (*CidContactOutput, error) {
	resp, err := klm.client.Get(fmt.Sprintf(cidContactUrl, cid))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	out := &CidContactOutput{
		Status: resp.StatusCode,
	}
	if resp.StatusCode != http.StatusOK {
		return out, nil
	}

	if resp.StatusCode == http.StatusOK {
		bz, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		out.Response = string(bz)

		if strings.Contains(out.Response, "dag.w3s") || strings.Contains(out.Response, "dag.house") {
			out.IsDagHouse = true
		} else if strings.Contains(out.Response, "pinata.cloud") {
			out.IsPinata = true
		}
	}
	return out, nil
}
