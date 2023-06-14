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
	// Define flags
	count := flag.Int("count", 0, "Count of requests to send to each component")
	fileName := flag.String("replay_file", "", "Name of replay file to use")
	nRuns := flag.Int("n_runs", 0, "Number of times to run the test")

	// Parse the flags
	flag.Parse()
	c := *count
	f := *fileName
	n := *nRuns
	fmt.Printf("count: %d, fileName: %s, nRuns:%d\n", c, f, n)
	if c == 0 || len(f) == 0 || n == 0 {
		fmt.Printf("Usage: onion -count <count> -replay_file <replay_file> -n_runs <n_runs>\n")
		os.Exit(1)
	}

	cfg := getConfig()
	fmt.Printf("parsed host:ports are:\n <Lassie> %s \n <L1Shim> %s \n <L1Nginx> %s\n", cfg.LassieHostPort, cfg.L1ShimHostPort, cfg.L1NginxHostPort)
	reqs := make(map[string]onion.URLsToTest)

	bifrostReqUrls := readBifrostReqURLs(f)
	ub := onion.NewURLBuilder(cfg.LassieHostPort, cfg.L1ShimHostPort, cfg.L1NginxHostPort)

	for _, u := range bifrostReqUrls {
		o := ub.BuildURLsToTest(u)
		key := o.Path
		reqs[key] = o
		if len(reqs) == c {
			break
		}
	}
	if len(reqs) < c {
		fmt.Printf("Not enough requests to send to components. Requested: %d, Available: %d\n", c, len(reqs))
		os.Exit(1)
	}

	for i := 0; i < n; i++ {
		dir := fmt.Sprintf("results-%d", i+1)
		err := os.MkdirAll(dir, 0755)
		if err != nil {
			panic(err)
		}
		re := onion.NewRequestExecutor(reqs, i+1, dir)
		re.Execute()
		re.WriteResultsToFile()
		re.WriteMismatchesToFile()
	}
}

func readBifrostReqURLs(fileName string) []string {
	file, err := os.Open(fileName)
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