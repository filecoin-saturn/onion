package main

import (
	"bufio"
	"flag"
	"fmt"
	"github.com/filecoin-saturn/onion"
	"github.com/pelletier/go-toml"
	"github.com/prometheus/client_golang/prometheus"
	"io"
	"net"
	"os"
	"strings"
)

var (
	httpResponseStatus = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_response_status",
			Help: "HTTP Response Status",
		},
		[]string{"system", "code"},
	)
)

func init() {
	prometheus.MustRegister(httpResponseStatus)
}

type Config struct {
	LassieHostPort  string
	L1ShimHostPort  string
	L1NginxHostPort string
}

func main() {
	flag.Parse()

	cfg := getConfig()
	fmt.Printf("parsed host:ports are:\n <Lassie> %s \n <L1Shim> %s \n <L1Nginx> %s\n", cfg.LassieHostPort, cfg.L1ShimHostPort, cfg.L1NginxHostPort)
	reqs := make(map[string]onion.URLsToTest)

	bifrostReqUrls := readBifrostReqURLs()
	ub := onion.NewURLBuilder(cfg.LassieHostPort, cfg.L1ShimHostPort, cfg.L1NginxHostPort)

	for _, u := range bifrostReqUrls[:20] {
		o := ub.BuildURLsToTest(u)
		key := o.Path
		reqs[key] = o
	}

	err := os.MkdirAll("results", 0755)
	if err != nil {
		panic(err)
	}

	re := onion.NewRequestExecutor(reqs)
	re.Execute()
	re.WriteResultsToFile("results/results.json")
	re.WriteMismatchesToFile()
}

func readBifrostReqURLs() []string {
	file, err := os.Open("replay_logs_car_no_range.tsv")
	if err != nil {
		panic(fmt.Errorf("failed to open replay logs: %w", err))
	}
	defer file.Close()

	var bifrostReqUrls []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Split(line, "\t")

		u := fields[20]
		if len(u) == 0 {
			panic(fmt.Errorf("invalid bifrost url: %s", u))
		}

		if strings.Contains(u, "ipfs-404") {
			continue
		}

		bifrostReqUrls = append(bifrostReqUrls, u)
	}
	return bifrostReqUrls
}

func getConfig() Config {
	type TomlConfig struct {
		LassieIP   string
		LassiePort int64

		L1ShimIP   string
		L1ShimPort int64

		L1NginxIP   string
		L1NginxPort int64
	}

	f, err := os.Open("config.toml")
	if err != nil {
		panic(fmt.Errorf("failed to open config.toml: %s", err))
	}
	bz, err := io.ReadAll(f)
	if err != nil {
		panic(fmt.Errorf("failed to read config.toml: %s", err))
	}

	var cfg TomlConfig
	if err := toml.Unmarshal(bz, &cfg); err != nil {
		panic(fmt.Errorf("failed to unmarshal config.toml: %s", err))
	}

	if net.ParseIP(cfg.LassieIP) == nil {
		panic(fmt.Errorf("invalid lassie ip: %s", cfg.LassieIP))
	}
	if net.ParseIP(cfg.L1ShimIP) == nil {
		panic(fmt.Errorf("invalid l1 shim ip: %s", cfg.L1ShimIP))
	}
	if net.ParseIP(cfg.L1NginxIP) == nil {
		panic(fmt.Errorf("invalid l1 nginx ip: %s", cfg.L1NginxIP))
	}

	if cfg.LassiePort <= 0 || cfg.LassiePort > 65535 {
		panic(fmt.Errorf("invalid lassie port: %d", cfg.LassiePort))
	}
	if cfg.L1ShimPort <= 0 || cfg.L1ShimPort > 65535 {
		panic(fmt.Errorf("invalid l1 shim port: %d", cfg.L1ShimPort))
	}
	if cfg.L1NginxPort <= 0 || cfg.L1NginxPort > 65535 {
		panic(fmt.Errorf("invalid l1 nginx port: %d", cfg.L1NginxPort))
	}

	return Config{
		LassieHostPort:  fmt.Sprintf("%s:%d", cfg.LassieIP, cfg.LassiePort),
		L1ShimHostPort:  fmt.Sprintf("%s:%d", cfg.L1ShimIP, cfg.L1ShimPort),
		L1NginxHostPort: fmt.Sprintf("%s:%d", cfg.L1NginxIP, cfg.L1NginxPort),
	}
}
