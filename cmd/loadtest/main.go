package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	target := flag.String("url", "http://localhost:8888/1", "target url")
	method := flag.String("method", http.MethodGet, "http method")
	body := flag.String("body", "", "request body")
	concurrency := flag.Int("c", 32, "concurrency")
	requests := flag.Int("n", 1000, "total requests")
	contentType := flag.String("content-type", "application/json", "content type")
	flag.Parse()

	if *concurrency <= 0 || *requests <= 0 {
		panic("concurrency and requests must be positive")
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	jobs := make(chan int)
	latencies := make([]time.Duration, 0, *requests)
	latencyCh := make(chan time.Duration, *requests)
	var success int64
	var failed int64

	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < *concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range jobs {
				begin := time.Now()
				statusCode, err := doRequest(client, *method, *target, *body, *contentType)
				latencyCh <- time.Since(begin)
				if err != nil || statusCode >= http.StatusInternalServerError {
					atomic.AddInt64(&failed, 1)
					continue
				}
				atomic.AddInt64(&success, 1)
			}
		}()
	}

	for i := 0; i < *requests; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	close(latencyCh)
	elapsed := time.Since(start)

	for latency := range latencyCh {
		latencies = append(latencies, latency)
	}
	sort.Slice(latencies, func(i, j int) bool {
		return latencies[i] < latencies[j]
	})

	fmt.Printf("Requests: %d\n", *requests)
	fmt.Printf("Concurrency: %d\n", *concurrency)
	fmt.Printf("Success: %d\n", success)
	fmt.Printf("Failed: %d\n", failed)
	fmt.Printf("Elapsed: %s\n", elapsed)
	fmt.Printf("QPS: %.2f\n", float64(*requests)/elapsed.Seconds())
	fmt.Printf("P50: %s\n", percentile(latencies, 50))
	fmt.Printf("P95: %s\n", percentile(latencies, 95))
	fmt.Printf("P99: %s\n", percentile(latencies, 99))
}

func doRequest(client *http.Client, method, target, body, contentType string) (int, error) {
	var reader io.Reader
	if body != "" {
		reader = bytes.NewBufferString(body)
	}
	req, err := http.NewRequest(method, target, reader)
	if err != nil {
		return 0, err
	}
	if body != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

func percentile(values []time.Duration, p int) time.Duration {
	if len(values) == 0 {
		return 0
	}
	if p <= 0 {
		return values[0]
	}
	if p >= 100 {
		return values[len(values)-1]
	}

	index := (len(values)*p + 99) / 100
	if index <= 0 {
		index = 1
	}
	return values[index-1]
}
