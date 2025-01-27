package main

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
	"path"
	"syscall"
	"time"

	"labench/bench"

	yaml "gopkg.in/yaml.v2"
)

type benchParams struct {
	RequestRatePerSec uint64        `yaml:"RequestRatePerSec"`
	Clients           uint64        `yaml:"Clients"`
	WarmUpDuration    time.Duration `yaml:"WarmUpDuration"`
	Duration          time.Duration `yaml:"Duration"`
	BaseLatency       time.Duration `yaml:"BaseLatency"`
	RequestTimeout    time.Duration `yaml:"RequestTimeout"`
	ReuseConnections  bool          `yaml:"ReuseConnections"`
	DontLinger        bool          `yaml:"DontLinger"`
	OutputJSON        bool          `yaml:"OutputJSON"`
	TightTicker       bool          `yaml:"TightTicker"`
	Insecure          bool          `yaml:"Insecure"`
}

type config struct {
	Params   benchParams         `yaml:",inline"`
	Protocol string              `yaml:"Protocol"`
	Request  WebRequesterFactory `yaml:"Request"`
	Output   string              `yaml:"OutFile"`
}

func maybePanic(err error) {
	if err != nil {
		log.Panic(err)
	}
}

func assert(cond bool, err string) {
	if !cond {
		log.Panic(errors.New(err))
	}
}

func main() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	configFile := "labench.yaml"
	if len(os.Args) > 1 {
		assert(len(os.Args) == 2, fmt.Sprintf("Usage: %s [config.yaml]\n\tThe default config file name is: %s", os.Args[0], configFile))
		configFile = os.Args[1]
	}

	configBytes, err := ioutil.ReadFile(configFile)
	maybePanic(err)

	var conf config
	err = yaml.Unmarshal(configBytes, &conf)
	maybePanic(err)

	// fmt.Printf("%+v\n", conf)
	fmt.Println("timeStart =", time.Now().UTC().Add(-5*time.Second).Truncate(time.Second))

	if conf.Request.ExpectedHTTPStatusCode == 0 {
		conf.Request.ExpectedHTTPStatusCode = 200
	}

	if conf.Request.HTTPMethod == "" {
		if conf.Request.Body == "" && conf.Request.BodyFile == "" {
			conf.Request.HTTPMethod = http.MethodGet
		} else {
			conf.Request.HTTPMethod = http.MethodPost
		}
	}

	if conf.Protocol == "" {
		conf.Protocol = "HTTP/1.1"
	}

	fmt.Println("Protocol:", conf.Protocol)

	switch conf.Protocol {
	case "HTTP/2":
		initHTTP2Client(conf.Params.RequestTimeout, conf.Params.DontLinger, conf.Params.Insecure)

	default:
		initHTTPClient(conf.Params.ReuseConnections, conf.Params.RequestTimeout, conf.Params.DontLinger, conf.Params.Insecure)
	}

	if conf.Params.RequestTimeout == 0 {
		conf.Params.RequestTimeout = 10 * time.Second
	}

	if conf.Params.Clients == 0 {
		clients := conf.Params.RequestRatePerSec * uint64(math.Ceil(conf.Params.RequestTimeout.Seconds()))
		clients += clients / 5 // add 20%
		conf.Params.Clients = clients
		fmt.Println("Clients:", clients)
	}

	done := make(chan struct{}, 1)
	go func() {
	loop:
		for {
			select {
			case c := <-sigChan:
				fmt.Println("Receive signal", c.String())
				done <- struct{}{}
			case <-done:
				break loop
			}
		}
	}()
	benchmark := bench.NewBenchmark(&conf.Request, conf.Params.RequestRatePerSec, conf.Params.Clients, conf.Params.Duration, conf.Params.WarmUpDuration, conf.Params.BaseLatency)
	summary, err := benchmark.Run(done, conf.Params.OutputJSON, conf.Params.TightTicker)
	maybePanic(err)
	close(done)

	fmt.Println("timeEnd   =", time.Now().UTC().Add(5*time.Second).Round(time.Second))

	fmt.Println(summary)

	outfile := conf.Output
	if outfile == "" {
		outfile = "out/res.hgrm"
	}

	err = os.MkdirAll(path.Dir(outfile), os.ModeDir|os.ModePerm)
	maybePanic(err)

	err = summary.GenerateLatencyDistribution(bench.Logarithmic, outfile)
	maybePanic(err)
}
