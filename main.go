package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"halloween-sims/templates"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/patrickmn/go-cache"
	"github.com/starfederation/datastar-go/datastar"
)

// Session represents a user session
type Session struct {
	SessionID string
	RoomID    string
}

type Needs struct {
	Bladder     int `json:"bladder"`
	BladderRate int `json:"bladderRate"`
	Fun         int `json:"fun"`
	FunRate     int `json:"funRate"`
	Hunger      int `json:"hunger"`
	HungerRate  int `json:"hungerRate"`
	Social      int `json:"social"`
	SocialRate  int `json:"socialRate"`
	Energy      int `json:"energy"`
	EnergyRate  int `json:"energyRate"`
	Hygeine     int `json:"hygeine"`
	HygeineRate int `json:"hygeineRate"`
}

// Global cache for session management
// Key: session cookie, Value: room UUID
var sessionCache = cache.New(2*time.Hour, 10*time.Minute)

// generateUUID creates a simple UUID-like string
func generateUUID() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

// createSession creates a new session with both session cookie and room UUID
func createSession() (string, string) {
	sessionID := generateUUID()
	roomID := generateUUID()

	// Store session cookie -> room UUID mapping in cache
	sessionCache.Set(sessionID, roomID, cache.DefaultExpiration)

	return sessionID, roomID
}

// getRoomID retrieves the room ID for a given session cookie
func getRoomID(sessionID string) (string, bool) {
	if roomID, found := sessionCache.Get(sessionID); found {
		return roomID.(string), true
	}
	return "", false
}

// setSessionCookie sets a session cookie in the HTTP response
func setSessionCookie(c echo.Context, sessionID string) {
	cookie := &http.Cookie{
		Name:     "session_id",
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   false, // Set to true in production with HTTPS
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(2 * time.Hour),
	}
	c.SetCookie(cookie)
}

// getSessionCookie retrieves the session cookie from the HTTP request
func getSessionCookie(c echo.Context) (string, error) {
	cookie, err := c.Cookie("session_id")
	if err != nil {
		return "", err
	}
	return cookie.Value, nil
}

// serveHTML serves an HTML file with proper content type
func serveHTML(c echo.Context, filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return c.String(http.StatusNotFound, "File not found")
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		return c.String(http.StatusInternalServerError, "Error reading file")
	}

	return c.HTMLBlob(http.StatusOK, content)
}

// serveCSS serves CSS files
func serveCSS(c echo.Context, filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return c.String(http.StatusNotFound, "File not found")
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		return c.String(http.StatusInternalServerError, "Error reading file")
	}

	return c.Blob(http.StatusOK, "text/css", content)
}

func main() {
	opts := &server.Options{
		// DontListen: true, // We want this in process only
	}
	ns, err := server.NewServer(opts)

	if err != nil {
		panic(err)
	}

	go ns.Start()

	if !ns.ReadyForConnections(4 * time.Second) {
		panic("nats failed")
	}
	defer ns.Shutdown()

	nc, err := nats.Connect(ns.ClientURL())

	if err != nil {
		panic(err)
	}

	e := echo.New()

	// Middleware
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Use(middleware.CORS())

	// Add timeout middleware for better connection handling
	e.Use(middleware.TimeoutWithConfig(middleware.TimeoutConfig{
		Timeout: 0, // No timeout for SSE connections
	}))

	// Health check endpoint
	e.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]interface{}{
			"status":    "healthy",
			"timestamp": time.Now().Unix(),
		})
	})

	// Serve CSS files
	e.GET("/styles.css", func(c echo.Context) error {
		return serveCSS(c, "styles.css")
	})

	// Root route - create session and redirect to room
	e.GET("/", func(c echo.Context) error {
		sessionID, roomID := createSession()
		setSessionCookie(c, sessionID)
		return c.Redirect(http.StatusSeeOther, "/"+roomID)
	})

	// Room-based routes (using room UUID in URL)
	// Public read-only access - no session required
	e.GET("/:roomID", func(c echo.Context) error {
		roomID := c.Param("roomID")

		// For now, we'll allow access to any room ID
		// In the future, you might want to validate that the room exists
		// but for read-only access, we don't need session validation

		// Create default sim data
		simData := templates.SimData{
			Bladder: 35,
			Fun:     95,
			Hunger:  28,
			Social:  12,
			Energy:  70,
			Hygeine: 90,
		}

		// Set content type and render using templ template
		c.Response().Header().Set("Content-Type", "text/html; charset=utf-8")
		return templates.IndexPage(roomID, simData).Render(c.Request().Context(), c.Response().Writer)
	})
	e.GET("/:roomID/connect", func(c echo.Context) error {
		roomID := c.Param("roomID")
		log.Printf("Client connected to room: %s", roomID)

		// Set SSE headers for better connection stability
		c.Response().Header().Set("Content-Type", "text/event-stream")
		c.Response().Header().Set("Cache-Control", "no-cache")
		c.Response().Header().Set("Connection", "keep-alive")
		c.Response().Header().Set("Access-Control-Allow-Origin", "*")
		c.Response().Header().Set("Access-Control-Allow-Headers", "Cache-Control")

		sse := datastar.NewSSE(c.Response().Writer, c.Request())

		// Create a channel to handle graceful shutdown
		done := make(chan struct{})

		sub, err := nc.Subscribe("sim."+roomID, func(m *nats.Msg) {
			// Check if the context is still valid before trying to patch signals
			select {
			case <-c.Request().Context().Done():
				log.Printf("Context cancelled for room %s, stopping message processing", roomID)
				return
			case <-done:
				log.Printf("Connection closed for room %s, stopping message processing", roomID)
				return
			default:
				// Context is still valid, proceed with patching signals
				if err := sse.PatchSignals(m.Data); err != nil {
					log.Printf("Failed to patch signals for room %s: %v", roomID, err)
					close(done)
					return
				}
				// Only flush if the response writer is still valid
				if c.Response().Writer != nil {
					c.Response().Flush()
				}
			}
		})
		if err != nil {
			log.Printf("Failed to subscribe to NATS for room %s: %v", roomID, err)
			return c.String(http.StatusInternalServerError, "Failed to connect")
		}
		defer func() {
			log.Printf("Unsubscribing from room %s", roomID)
			sub.Unsubscribe()
		}()

		// Send initial signals
		initialSignals := map[string]any{
			"bladder": 95,
			"fun":     95,
			"hunger":  28,
			"social":  12,
			"energy":  70,
			"hygeine": 90,
		}
		signalsJSON, _ := json.Marshal(initialSignals)
		if err := sse.PatchSignals(signalsJSON); err != nil {
			log.Printf("Failed to send initial signals for room %s: %v", roomID, err)
			return err
		}

		c.Response().Flush()

		// Start heartbeat ticker
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		go func() {
			for {
				select {
				case <-ticker.C:
					// Send heartbeat to keep connection alive
					select {
					case <-c.Request().Context().Done():
						return
					case <-done:
						return
					default:
						heartbeat := map[string]any{"heartbeat": time.Now().Unix()}
						heartbeatJSON, _ := json.Marshal(heartbeat)
						if err := sse.PatchSignals(heartbeatJSON); err != nil {
							log.Printf("Failed to send heartbeat for room %s: %v", roomID, err)
							close(done)
							return
						}
						if c.Response().Writer != nil {
							c.Response().Flush()
						}
					}
				case <-done:
					return
				}
			}
		}()

		// Wait for the connection to close
		select {
		case <-c.Request().Context().Done():
			log.Printf("Client disconnected from room %s (context cancelled)", roomID)
		case <-done:
			log.Printf("Client disconnected from room %s (connection closed)", roomID)
		}

		return nil
	})

	e.GET("/:roomID/control", func(c echo.Context) error {
		roomID := c.Param("roomID")

		// Get session cookie
		sessionID, err := getSessionCookie(c)
		if err != nil {
			return c.String(http.StatusNotFound, "Unknown sim")
		}

		// Verify session and get associated room ID
		cachedRoomID, exists := getRoomID(sessionID)
		if !exists || cachedRoomID != roomID {
			return c.String(http.StatusForbidden, "You don't have access to this sim")
		}

		// Create default sim data
		simData := templates.SimData{
			Bladder: 35,
			Fun:     95,
			Hunger:  28,
			Social:  12,
			Energy:  70,
			Hygeine: 90,
		}

		// Set content type and render using templ template
		c.Response().Header().Set("Content-Type", "text/html; charset=utf-8")
		return templates.ControlPage(roomID, simData).Render(c.Request().Context(), c.Response().Writer)
	})

	e.POST("/:roomID/control", func(c echo.Context) error {
		roomID := c.Param("roomID")

		// Get session cookie
		sessionID, err := getSessionCookie(c)
		if err != nil {
			return c.String(http.StatusNotFound, "Unknown sim")
		}

		// Verify session and get associated room ID
		cachedRoomID, exists := getRoomID(sessionID)
		if !exists || cachedRoomID != roomID {
			return c.String(http.StatusForbidden, "You don't have access to this sim")
		}

		needs := Needs{}
		datastar.ReadSignals(c.Request(), &needs)
		data, _ := json.Marshal(needs)
		nc.Publish("sim."+roomID, data)
		return c.String(http.StatusOK, "Control POST request received")
	})

	// Start server
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	fmt.Printf("Server starting on port %s\n", port)
	fmt.Printf("Visit http://localhost:%s to start a new session\n", port)

	e.Logger.Fatal(e.Start(":" + port))
}
