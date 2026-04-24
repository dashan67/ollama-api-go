package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"time"
)

type Model struct {
	Name       string `json:"name"`
	Model      string `json:"model"`
	ModifiedAt string `json:"modified_at"`
	Size       int    `json:"size"`
}

type Config struct {
	APIKey   string  `json:"api_key"`
	OllamaAPI string `json:"ollama_api"`
	Port     int     `json:"port"`
	Models   []Model `json:"models"`
}

var cfg Config

func loadConfig(path string) error {
	b, err := ioutil.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return err
	}
	if cfg.OllamaAPI == "" {
		cfg.OllamaAPI = "https://ollama.com/api"
	}
	if cfg.Port == 0 {
		cfg.Port = 11434
	}
	return nil
}

func tagsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]interface{}{"models": cfg.Models}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(resp); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func proxyPostHandler(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Read incoming body
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "failed to read body: "+err.Error(), http.StatusBadRequest)
			return
		}

		client := &http.Client{Timeout: 0}
		upstreamURL := fmt.Sprintf("%s%s", cfg.OllamaAPI, path)
		req, err := http.NewRequest("POST", upstreamURL, io.NopCloser(bytesReader(body)))
		if err != nil {
			http.Error(w, "failed to create upstream request: "+err.Error(), http.StatusInternalServerError)
			return
		}
		// Forward Content-Type
		if ct := r.Header.Get("Content-Type"); ct != "" {
			req.Header.Set("Content-Type", ct)
		} else {
			req.Header.Set("Content-Type", "application/json")
		}
		// Authorization header from config if present
		if cfg.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
		}

		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, "upstream request failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		// Copy status code and headers
		for k, v := range resp.Header {
			for _, vv := range v {
				w.Header().Add(k, vv)
			}
		}
		w.WriteHeader(resp.StatusCode)

		// Stream body
		buf := make([]byte, 32*1024)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					return
				}
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
			if err != nil {
				if err == io.EOF {
					return
				}
				log.Printf("error while copying upstream body: %v", err)
				return
			}
		}
	}
}

// bytesReader returns an io.Reader from a byte slice without importing bytes everywhere
func bytesReader(b []byte) *sliceReader {
	return &sliceReader{b: b}
}

type sliceReader struct{ b []byte; i int }

func (s *sliceReader) Read(p []byte) (n int, err error) {
	if s.i >= len(s.b) {
		return 0, io.EOF
	}
	n = copy(p, s.b[s.i:])
	s.i += n
	return n, nil
}

func main() {
	cfgPath := os.Getenv("OLLAMA_CONFIG")
	if cfgPath == "" {
		cfgPath = "./config.json"
	}
	if err := loadConfig(cfgPath); err != nil {
		log.Fatalf("failed to load config (%s): %v", cfgPath, err)
	}

	http.HandleFunc("/api/tags", tagsHandler)
	http.HandleFunc("/api/generate", proxyPostHandler("/generate"))
	http.HandleFunc("/api/chat", proxyPostHandler("/chat"))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Ollama Compatible Server Running"))
	})

	addr := fmt.Sprintf("0.0.0.0:%d", cfg.Port)
	server := &http.Server{Addr: addr, ReadTimeout: 0, WriteTimeout: 0, IdleTimeout: 0}
	log.Printf("starting server on %s", addr)
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("server exited: %v", err)
	}
}
