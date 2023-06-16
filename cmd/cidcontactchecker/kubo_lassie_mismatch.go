package main

import (
	"encoding/json"
	"fmt"
	"github.com/filecoin-saturn/onion"
	"io"
	"net/http"
	"strings"
	"time"
)

var cidContactUrl = "https://cid.contact/cid/%s"

type CidContactChecker struct {
	client     http.Client
	mismatches []string
	src        string
	target     string
}

type CidContactOutput struct {
	Status     int
	Response   string
	IsDagHouse bool
}

func NewCidContactChecker(mismatches []string, src, target string) *CidContactChecker {
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
		src:        src,
		target:     target,
	}
}

func (klm *CidContactChecker) Check() {
	type Summary struct {
		NotFoundOnCidContact int
		DAGHouseCid          int
		Others               int
	}

	sum := Summary{}

	for _, path := range klm.mismatches {
		// check cid.contact
		cid := onion.ParseCidFromPath(path)
		cc, err := klm.GetCidContactResponse(cid)
		if err != nil {
			panic(err)
		}
		if cc.Status != http.StatusOK {
			fmt.Println("cid.contact returned non 200 status code", cc.Status)
			sum.NotFoundOnCidContact++
		} else if cc.IsDagHouse {
			sum.DAGHouseCid++
		} else {
			sum.Others++
		}
	}

	fmt.Println("\n--- Summary of mismatches---")
	fmt.Printf("From a total of %d cids that were available on %s but not from %s\n", len(klm.mismatches), klm.src, klm.target)
	bz, err := json.MarshalIndent(sum, "", " ")
	if err != nil {
		panic(err)
	}
	fmt.Println(string(bz))
}

func (klm *CidContactChecker) GetCidContactResponse(cid string) (*CidContactOutput, error) {
	resp, err := klm.client.Get(fmt.Sprintf(cidContactUrl, cid))
	if err != nil {
		fmt.Printf("Error checking cid.contact: %s\n", err)
		return nil, err
	}
	defer resp.Body.Close()

	out := &CidContactOutput{
		Status: resp.StatusCode,
	}
	if resp.StatusCode != http.StatusOK {
		bz, err := io.ReadAll(resp.Body)
		if err != nil {
			panic(err)
		}
		fmt.Printf("error for cid %s is %s", cid, string(bz))
		return out, nil
	}

	if resp.StatusCode == http.StatusOK {
		bz, err := io.ReadAll(resp.Body)
		if err != nil {
			panic(err)
		}
		out.Response = string(bz)

		if strings.Contains(out.Response, "dag.w3s") || strings.Contains(out.Response, "dag.house") {
			out.IsDagHouse = true
		}
	}
	return out, nil
}
