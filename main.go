package main

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

type RequestStats struct {
	successCount int
	failureCount int
	totalTime    time.Duration
	mutex        sync.Mutex
}

func (stats *RequestStats) addSuccess(duration time.Duration) {
	stats.mutex.Lock()
	defer stats.mutex.Unlock()
	stats.successCount++
	stats.totalTime += duration
}

func (stats *RequestStats) addFailure() {
	stats.mutex.Lock()
	defer stats.mutex.Unlock()
	stats.failureCount++
}

func getContainerMemoryUsage(containerId string) (uint64, error) {
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return 0, fmt.Errorf("error creating Docker client: %v", err)
	}
	defer cli.Close()

	stats, err := cli.ContainerStats(ctx, containerId, false)
	if err != nil {
		return 0, fmt.Errorf("error getting container stats: %v", err)
	}
	defer stats.Body.Close()

	var containerStats container.StatsResponse
	if err := containerStats.FromJSON(stats.Body); err != nil {
		return 0, fmt.Errorf("error parsing container stats: %v", err)
	}

	return containerStats.MemoryStats.Usage, nil
}

func makeRequest(url string, requestNum int, imageData []byte, imageName string, stats *RequestStats, wg *sync.WaitGroup, bearerToken string) {
	defer wg.Done()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormFile("file[]", imageName)
	if err != nil {
		fmt.Printf("Error creating form file: %v\n", err)
		stats.addFailure()
		return
	}

	_, err = part.Write(imageData)
	if err != nil {
		fmt.Printf("Error writing image data: %v\n", err)
		stats.addFailure()
		return
	}
	writer.Close()

	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		fmt.Printf("Error creating request: %v\n", err)
		stats.addFailure()
		return
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Origin", "http://axxonnet.test")
	req.Header.Set("Referer", "http://axxonnet.test/")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")

	req.Header.Set("Authorization", "Bearer "+bearerToken)
	req.Header.Set("Time-Zone", "Europe/Moscow")

	startTime := time.Now()
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Request %d failed: %v\n", requestNum, err)
		stats.addFailure()
		return
	}
	defer resp.Body.Close()

	duration := time.Since(startTime)

	if resp.StatusCode == http.StatusOK {
		stats.addSuccess(duration)
		if requestNum%50 == 0 {
			fmt.Printf("Request %d completed successfully in %v\n", requestNum, duration)
		}
	} else {
		fmt.Printf("Request %d failed with status: %d\n", requestNum, resp.StatusCode)
		stats.addFailure()
	}
}

func loadImagesFromFolder(folderPath string) ([][]byte, []string, error) {
	var images [][]byte
	var imageNames []string

	files, err := os.ReadDir(folderPath)
	if err != nil {
		return nil, nil, fmt.Errorf("error reading directory: %v", err)
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		ext := filepath.Ext(file.Name())
		if ext != ".jpg" && ext != ".jpeg" && ext != ".png" {
			continue
		}

		filePath := filepath.Join(folderPath, file.Name())
		imageData, err := os.ReadFile(filePath)
		if err != nil {
			fmt.Printf("Warning: couldn't read image %s: %v\n", file.Name(), err)
			continue
		}

		images = append(images, imageData)
		imageNames = append(imageNames, file.Name())
	}

	if len(images) == 0 {
		return nil, nil, fmt.Errorf("no valid images found in folder")
	}

	return images, imageNames, nil
}

func main() {
	url := "http://axxonnet.test/api/v1/faceLists/1/faces/bulk"
	imageFolder := "1"
	totalRequests := 1000
	concurrentRequests := 10

	containerId := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJVc2VySUQiOjEsIkNsaWVudElEIjoiYmYxMDc2NzhjNTU0Mzg3Yzg1MDg1MjE1MjcxY2MyMzgiLCJUeXBlIjoiYWNjZXNzVG9rZW4iLCJWZXJzaW9uIjoidjIiLCJDcmVhdGVkQXQiOiIyMDI0LTEyLTEzVDA5OjA3OjE5LjQxMzQwMjAxOVoiLCJleHAiOjE3MzQxNjcyMzksImlhdCI6MTczNDA4MDgzOSwiaXNzIjoiQ2xvdWQifQ.BzFxzfBDf0NZ8cE88J8-YRbO8JSYZGZnJc30nXiAGjY"

	bearerToken := "your-bearer-token-here"

	initialMemory, err := getContainerMemoryUsage(containerId)
	if err != nil {
		fmt.Printf("Error getting initial memory usage: %v\n", err)
		return
	}

	images, imageNames, err := loadImagesFromFolder(imageFolder)
	if err != nil {
		fmt.Printf("Error loading images: %v\n", err)
		return
	}

	fmt.Printf("Loaded %d images from folder\n", len(images))

	stats := &RequestStats{}
	var wg sync.WaitGroup

	semaphore := make(chan struct{}, concurrentRequests)

	startTime := time.Now()

	for i := 0; i < totalRequests; i++ {
		wg.Add(1)
		semaphore <- struct{}{}

		imageIndex := i % len(images)

		go func(requestNum int, imageData []byte, imageName string) {
			defer func() { <-semaphore }()
			makeRequest(url, requestNum, imageData, imageName, stats, &wg, bearerToken)

			time.Sleep(20 * time.Millisecond)
		}(i, images[imageIndex], imageNames[imageIndex])
	}

	wg.Wait()
	totalDuration := time.Since(startTime)

	finalMemory, err := getContainerMemoryUsage(containerId)
	if err != nil {
		fmt.Printf("Error getting final memory usage: %v\n", err)
		return
	}

	memoryDifference := finalMemory - initialMemory

	fmt.Printf("\n=== Результаты тестирования ===\n")
	fmt.Printf("Всего запросов: %d\n", totalRequests)
	fmt.Printf("Успешных запросов: %d\n", stats.successCount)
	fmt.Printf("Неудачных запросов: %d\n", stats.failureCount)
	fmt.Printf("Общее время выполнения: %v\n", totalDuration)
	fmt.Printf("Среднее время запроса: %v\n", stats.totalTime/time.Duration(stats.successCount))
	fmt.Printf("Запросов в секунду: %.2f\n", float64(totalRequests)/totalDuration.Seconds())

	fmt.Printf("\n=== Использование памяти ===\n")
	fmt.Printf("Начальное использование памяти: %.2f MB\n", float64(initialMemory)/1024/1024)
	fmt.Printf("Конечное использование памяти: %.2f MB\n", float64(finalMemory)/1024/1024)
	fmt.Printf("Разница в использовании памяти: %.2f MB\n", float64(memoryDifference)/1024/1024)
}
