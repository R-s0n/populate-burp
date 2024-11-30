package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	Reset  = "\033[0m"
	Red    = "\033[31m"
	Green  = "\033[32m"
	Yellow = "\033[33m"
)

var (
	httpVerbs = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}
	sem       = make(chan struct{}, 10)
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: go run main.go <file_with_urls> <file_with_endpoints>")
		os.Exit(1)
	}

	rawUrls, err := readLines(os.Args[1])
	if err != nil {
		fmt.Printf("Failed to read URLs: %v\n", err)
		os.Exit(1)
	}

	endpoints, err := readLines(os.Args[2])
	if err != nil {
		fmt.Printf("Failed to read endpoints: %v\n", err)
		os.Exit(1)
	}

	urls := sanitizeAndDeduplicateURLs(rawUrls)

	proxy, err := url.Parse("http://localhost:8080")
	if err != nil {
		fmt.Printf("Failed to parse proxy: %v\n", err)
		os.Exit(1)
	}

	client := &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxy),
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	var wg sync.WaitGroup

	for _, u := range urls {
		for _, scheme := range []string{"http", "https"} {
			modifiedURL := switchScheme(u, scheme)
			for _, verb := range httpVerbs {
				wg.Add(1)
				go func(url, method string) {
					defer wg.Done()
					sendRequest(client, url, method)
				}(modifiedURL, verb)
			}
		}
	}
	wg.Wait()
	fmt.Println("All initial requests completed.")

	for _, endpoint := range endpoints {
		for _, u := range urls {
			for _, scheme := range []string{"http", "https"} {
				base, _ := url.Parse(switchScheme(u, scheme))
				endpointURL, _ := url.Parse(endpoint)
				modifiedURL := base.ResolveReference(endpointURL).String()
				for _, verb := range httpVerbs {
					wg.Add(1)
					go func(url, method string) {
						defer wg.Done()
						sendRequest(client, url, method)
					}(modifiedURL, verb)
				}
			}
		}
		wg.Wait()
	}
	fmt.Println("All endpoint requests completed.")
}

func sendRequest(client *http.Client, url, method string) {
	sem <- struct{}{}
	defer func() { <-sem }()

	maxRetries := 3
	for i := 0; i < maxRetries; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, method, url, nil)
		if err != nil {
			fmt.Printf("%s[%s] Failed to create request for %s: %v%s\n", Red, method, url, err, Reset)
			return
		}

		resp, err := client.Do(req)
		if err != nil {
			if i < maxRetries-1 {
				fmt.Printf("%s[%s] Timeout or error for %s. Retrying (%d/%d)...%s\n", Yellow, method, url, i+1, maxRetries, Reset)
				time.Sleep(2 * time.Second)
				continue
			}
			fmt.Printf("%s[%s] Error for %s after %d retries: %v%s\n", Red, method, url, maxRetries, err, Reset)
			return
		}
		defer resp.Body.Close()

		fmt.Printf("%s[%s] %s - Status: %s%s\n", Green, method, url, resp.Status, Reset)
		return
	}
}

func sanitizeAndDeduplicateURLs(rawUrls []string) []string {
	uniqueURLs := make(map[string]struct{})
	var sanitizedURLs []string

	for _, rawURL := range rawUrls {
		parsedURL, err := url.Parse(rawURL)
		if err != nil {
			fmt.Printf("%sSkipping invalid URL: %s (%v)%s\n", Red, rawURL, err, Reset)
			continue
		}

		if parsedURL.Scheme == "" || parsedURL.Host == "" {
			fmt.Printf("%sInvalid URL (missing scheme or host): %s%s\n", Red, rawURL, Reset)
			continue
		}

		parsedURL.Path = ""
		parsedURL.RawQuery = ""
		parsedURL.Fragment = ""

		cleanURL := parsedURL.String()
		if _, exists := uniqueURLs[cleanURL]; !exists {
			uniqueURLs[cleanURL] = struct{}{}
			sanitizedURLs = append(sanitizedURLs, cleanURL)
		}
	}
	return sanitizedURLs
}

func switchScheme(rawURL, scheme string) string {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		fmt.Printf("%sFailed to parse URL %s: %v%s\n", Red, rawURL, err, Reset)
		return rawURL
	}

	parsedURL.Scheme = scheme
	if parsedURL.Host == "" {
		fmt.Printf("%sInvalid URL (missing host): %s%s\n", Red, rawURL, Reset)
		return rawURL
	}

	return parsedURL.String()
}

func readLines(filePath string) ([]string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, strings.TrimSpace(scanner.Text()))
	}
	return lines, scanner.Err()
}
