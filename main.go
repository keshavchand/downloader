package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
)

func init() {
	log.SetFlags(log.Lshortfile)
}

type downloader struct {
	client *http.Client
}

type Status struct {
	Downloaded int
}

func (d *downloader) Download(request *http.Request, location io.Writer) error {
	client := d.client
	resp, err := client.Do(request)
	if err != nil {
		log.Println("Error while downloading", request.URL, "-", err)
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(location, resp.Body)
	return err
}

func GetFileSize(url string) (uint64, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodHead, url, nil)
	if err != nil {
		return 0, err
	}

	client := http.Client{}
	resp, err := client.Do(req)
	contentlength := resp.Header.Get("Content-Length")
	if contentlength == "" {
		return 0, errors.New("Content-Length not found")
	}
	length, err := strconv.ParseUint(contentlength, 10, 64)
	return length, err
}

func Exists(name string, override bool) bool {
	_, err := os.Stat(name)
	if err == nil {
		if override {
			return true
		}
		log.Println("File exists make sure the *override* flag is set to continue")
		return false
	} else if os.IsNotExist(err) {
		return true
	} else {
		log.Println("Error while checking if file exists", err)
		return false
	}
}

func main() {
	var url, name string
	var override bool
	var concurrencyLevel int

	var chunkSize uint64 = 10 * 1024 * 1024 // 1 MB

	flag.StringVar(&url, "url", "", "URL to download")
	flag.StringVar(&name, "name", "", "name of target file")
	flag.BoolVar(&override, "override", false, "override file")
	flag.IntVar(&concurrencyLevel, "conc", 10, "concurrency level (number of threads)")

	flag.Parse()

	if !Exists(name, override) {
		return
	}

	size, err := GetFileSize(url)
	if err != nil {
		log.Fatal(err)
	}

	file, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY, 0664)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	downloaders := make([]*downloader, concurrencyLevel)
	for idx := range downloaders {
		downloaders[idx] = &downloader{
			client: &http.Client{},
		}
	}

	status := make(chan Status, 1)
	defer close(status)

	var partCount uint64
	var wg sync.WaitGroup
	defer wg.Wait()

	go func() {
		totalDownloaded := 0
		for s := range status {
			totalDownloaded += s.Downloaded
			fmt.Printf("%.2f %% downloaded \r", float64(totalDownloaded)/float64(size))
		}
		fmt.Println("Download complete")
	}()

	for i := 0; i < concurrencyLevel; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for {
				// AddUint64 returns the new value
				partCount := atomic.AddUint64(&partCount, 1) - 1
				start := partCount * chunkSize
				if start >= size {
					return
				}
				// NOTE: Range is inclusive
				end := (partCount+1)*chunkSize - 1

				request, err := http.NewRequest(http.MethodGet, url, nil)
				if err != nil {
					log.Println(err)
					return
				}

				ranges := fmt.Sprintf("bytes=%d-%d", start, end)
				request.Header.Set("Range", ranges)
				offsetFile := io.NewOffsetWriter(file, int64(start))
				err = downloaders[i].Download(request, offsetFile)
				if err != nil {
					log.Println("Error Downloading: ", err)
					return
				}
				status <- Status{Downloaded: int(end - start + 1)}
			}
		}(i)
	}
}
