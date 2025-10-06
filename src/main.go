package main

import (
	"context"
	"errors"
	"media-roller/src/media"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
)

func main() {
	// setup routes
	router := chi.NewRouter()
	router.Route("/", func(r chi.Router) {
		router.Get("/", media.Index)
		router.Get("/fetch", media.FetchMedia)
		router.Get("/api/download", media.FetchMediaApi)
		router.Get("/download", media.ServeMedia)
		router.Get("/about", media.AboutIndex)
	})
	fileServer(router, "/static", "static/")

	// Print out all routes
	walkFunc := func(method string, route string, handler http.Handler, middlewares ...func(http.Handler) http.Handler) error {
		log.Info().Msgf("%s %s", method, route)
		return nil
	}
	// Panic if there is an error
	if err := chi.Walk(router, walkFunc); err != nil {
		log.Panic().Msgf("%s\n", err.Error())
	}

	media.GetInstalledVersion()
	go startYtDlpUpdater()

	// The HTTP Server
	server := &http.Server{Addr: ":3000", Handler: router}

	// Server run context
	serverCtx, serverStopCtx := context.WithCancel(context.Background())

	// Listen for syscall signals for process to interrupt/quit
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	go func() {
		<-sig

		// Shutdown signal with grace period of 30 seconds
		shutdownCtx, cancel := context.WithTimeout(serverCtx, 30*time.Second)
		defer cancel()

		go func() {
			<-shutdownCtx.Done()
			if errors.Is(shutdownCtx.Err(), context.DeadlineExceeded) {
				log.Fatal().Msg("graceful shutdown timed out.. forcing exit.")
			}
		}()

		// Trigger graceful shutdown
		err := server.Shutdown(shutdownCtx)
		if err != nil {
			log.Fatal().Err(err)
		}
		serverStopCtx()
	}()

	// Run the server
	err := server.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal().Err(err)
	}

	// Wait for server context to be stopped
	<-serverCtx.Done()
	log.Info().Msgf("Shutdown complete")
}

// startYtDlpUpdater will update the yt-dlp to the latest nightly version ever few hours
func startYtDlpUpdater() {
	log.Info().Msgf("yt-dlp version: %s", media.GetInstalledVersion())
	ticker := time.NewTicker(12 * time.Hour)

	// Do one update now
	_ = media.UpdateYtDlp()

	quit := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				_ = media.UpdateYtDlp()
				log.Info().Msgf("yt-dlp version: %s", media.GetInstalledVersion())
			case <-quit:
				ticker.Stop()
				return
			}
		}
	}()
}

func fileServer(r chi.Router, public string, static string) {
	if strings.ContainsAny(public, "{}*") {
		panic("FileServer does not permit URL parameters.")
	}

	root, _ := filepath.Abs(static)
	if _, err := os.Stat(root); os.IsNotExist(err) {
		panic("Static Documents Directory Not Found")
	}

	fs := http.StripPrefix(public, http.FileServer(http.Dir(root)))

	if public != "/" && public[len(public)-1] != '/' {
		r.Get(public, http.RedirectHandler(public+"/", http.StatusMovedPermanently).ServeHTTP)
		public += "/"
	}

	r.Get(public+"*", func(w http.ResponseWriter, r *http.Request) {
		file := strings.Replace(r.RequestURI, public, "/", 1)
		if _, err := os.Stat(root + file); os.IsNotExist(err) {
			http.ServeFile(w, r, path.Join(root, "index.html"))
			return
		}
		fs.ServeHTTP(w, r)
	})
}
