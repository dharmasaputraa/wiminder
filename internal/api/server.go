package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"wiminder/internal/calendarprov"
	"wiminder/internal/config"
	"wiminder/internal/secret"
	"wiminder/internal/store"
)

type RunResult struct{ Sent, Failed, Missed int }

type SchedulerRunner interface {
	RunOnce(ctx context.Context) (RunResult, error)
}

type Server struct {
	cfg       config.Config
	st        *store.Store
	key       []byte
	providers []calendarprov.Provider
	runner    SchedulerRunner
	engine    *gin.Engine
}

func NewServer(cfg config.Config, st *store.Store, providers []calendarprov.Provider) *Server {
	s := &Server{cfg: cfg, st: st, key: secret.DeriveKey(cfg.AppSecret), providers: providers}
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), slogMiddleware())

	r.GET("/healthz", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	r.GET("/readyz", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if err := st.Ping(ctx); err != nil {
			c.JSON(503, gin.H{"error": "db not ready"})
			return
		}
		c.JSON(200, gin.H{"ok": true})
	})
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))
	r.NoRoute(s.spaHandler())

	apiG := r.Group("/api/v1")
	if cfg.AuthMode == config.AuthDev {
		apiG.Use(devAuthMiddleware(st, cfg.AdminEmails))
	} else {
		mw, err := NewCFAccess(context.Background(), cfg, st)
		if err != nil {
			slog.Error("cfaccess init failed", "err", err)
			panic(err)
		}
		apiG.Use(mw)
	}

	apiG.GET("/me", s.handleMe)
	apiG.GET("/contacts", s.handleListContacts)
	apiG.POST("/contacts", s.handleCreateContact)
	apiG.GET("/contacts/:id", s.handleGetContact)
	apiG.PATCH("/contacts/:id", s.handleUpdateContact)
	apiG.DELETE("/contacts/:id", s.handleDeleteContact)
	apiG.POST("/contacts/:id/occasions", s.handleAddOccasion)
	apiG.PATCH("/occasions/:id", s.handleUpdateOccasion)
	apiG.DELETE("/occasions/:id", s.handleDeleteOccasion)
	// gin ≥1.7 routes static and param siblings: /occasions/types coexists
	// with /occasions/:id without a registration conflict.
	apiG.GET("/occasions/types", s.handleOccasionTypes)
	apiG.GET("/occasions/:id/prefs", s.handleGetOccasionPrefs)
	apiG.PUT("/occasions/:id/prefs", s.handleSetOccasionPrefs)
	apiG.DELETE("/occasions/:id/prefs", s.handleDeleteOccasionPrefs)
	apiG.PUT("/contacts/:id/prefs", s.handleSetPrefs)
	apiG.GET("/upcoming", s.handleUpcoming)
	apiG.POST("/upcoming/notify", s.handleUpcomingNotify)
	apiG.GET("/pawukon", s.handlePawukon)
	apiG.GET("/channels", s.handleListChannels)
	apiG.POST("/channels", s.handleCreateChannel)
	apiG.PATCH("/channels/:id", s.handlePatchChannel)
	apiG.DELETE("/channels/:id", s.handleDeleteChannel)
	apiG.POST("/channels/:id/test", s.handleChannelTest)
	apiG.GET("/settings", s.handleGetSettings)
	apiG.PUT("/settings", s.handlePutSettings)
	apiG.GET("/users", s.handleListUsers)             // admin
	apiG.POST("/scheduler/run", s.handleSchedulerRun) // admin; runner from Plan 3

	s.engine = r
	return s
}

// SetRunner is called by main after the scheduler is built (Plan 3).
func (s *Server) SetRunner(r SchedulerRunner) { s.runner = r }

func (s *Server) ServeHTTP(w http.ResponseWriter, req *http.Request) { s.engine.ServeHTTP(w, req) }

func slogMiddleware() gin.HandlerFunc {
	log := slog.Default()
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		log.Info("http", "method", c.Request.Method, "path", c.Request.URL.Path,
			"status", c.Writer.Status(), "dur", time.Since(start).Round(time.Millisecond).String())
	}
}

func mustUser(c *gin.Context) store.User { return c.MustGet("user").(store.User) }
