package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"time"
)

type key int

const (
	requestIDKey key = 0
)

var (
	listenAddr string
	healthy    int32
	GitCommit  string
	BuildDate  string
)

func main() {
	flag.StringVar(&listenAddr, "listen-addr", ":5000", "server listen address")
	flag.Parse()

	logger := log.New(os.Stdout, "http: ", log.LstdFlags)
	logger.Println("Server is starting...")

	router := http.NewServeMux()
	router.Handle("/", index())
	router.Handle("/healthz", healthz())

	nextRequestID := func() string {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}

	server := &http.Server{
		Addr:         listenAddr,
		Handler:      buildinfo()(tracing(nextRequestID)(logging(logger)(router))),
		ErrorLog:     logger,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  15 * time.Second,
	}

	done := make(chan bool)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt)

	go func() {
		<-quit
		logger.Println("Server is shutting down...")
		atomic.StoreInt32(&healthy, 0)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		server.SetKeepAlivesEnabled(false)
		if err := server.Shutdown(ctx); err != nil {
			logger.Fatalf("Could not gracefully shutdown the server: %v\n", err)
		}
		close(done)
	}()

	logger.Println("Server is ready to handle requests at", listenAddr)
	atomic.StoreInt32(&healthy, 1)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Fatalf("Could not listen on %s: %v\n", listenAddr, err)
	}

	<-done
	logger.Println("Server stopped")
}

func parseUploadPlugin(up string) map[string]string {
	creds := make(map[string]string)
	keyvalues := strings.Split(up, ";")
	for _, keyvalue := range keyvalues {
		if !strings.Contains(keyvalue, "=") {
			continue
		}
		kv := strings.SplitN(keyvalue, "=", 2)
		creds[kv[0]] = kv[1]
	}
	return creds
}

func index() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only handle the / endpoint here. This is also the catchall and could
		// be some other url that doesn't exist, if so, error.
		if r.URL.Path != "/" {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}
		//w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method == "GET" {
			fh, err := os.Open("wiki.html")
			if err != nil {
				http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
				return
			}
			defer fh.Close()
			w.WriteHeader(http.StatusOK)
			io.Copy(w, fh)
			return
		} else if r.Method != "POST" {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}

		if !strings.HasPrefix(r.Header["Content-Type"][0], "multipart/form-data;") {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}
		r.ParseMultipartForm(32 << 20)
		// Process creds first
		uploadplugin := r.FormValue("UploadPlugin")
		if uploadplugin == "" {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}
		creds := parseUploadPlugin(uploadplugin)
		// test creds["user"] and creds["password"]
		if creds["user"] != os.Getenv("AUTH_USER") || creds["password"] != os.Getenv("AUTH_PASS") {
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		}

		uffile, _, err := r.FormFile("userfile")
		if err != nil {
			log.Printf("Unable to handle userfile: %s\n", err)
			http.Error(w, "Unable to handle userfile", http.StatusInternalServerError)
			return
		}
		defer uffile.Close()
		if err != nil {
			log.Printf("Unable to read userfile: %s\n", err)
			http.Error(w, "Unable to read userfile", http.StatusInternalServerError)
			return
		}
		wiki, err := os.Create("wiki.html")
		if err != nil {
			log.Printf("Unable to open wiki.html for writing: %s\n", err)
			http.Error(w, "Unable to save wiki", http.StatusInternalServerError)
			return
		}
		defer wiki.Close()
		io.Copy(wiki, uffile)

		w.WriteHeader(http.StatusOK)
	})
}

func healthz() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.LoadInt32(&healthy) == 1 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	})
}

func logging(logger *log.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				requestID, ok := r.Context().Value(requestIDKey).(string)
				if !ok {
					requestID = "unknown"
				}
				logger.Println(requestID, r.Method, r.URL.Path, r.RemoteAddr, r.UserAgent())
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func tracing(nextRequestID func() string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := r.Header.Get("X-Request-Id")
			if requestID == "" {
				requestID = nextRequestID()
			}
			ctx := context.WithValue(r.Context(), requestIDKey, requestID)
			w.Header().Set("X-Request-Id", requestID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func buildinfo() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-License", "AGPLv3 http://www.gnu.org/licenses/agpl-3.0.txt")
			next.ServeHTTP(w, r)
		})
	}
}
