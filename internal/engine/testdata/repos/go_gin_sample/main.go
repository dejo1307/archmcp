package main

import "github.com/gin-gonic/gin"

// A gin server in the shape real ones take. Three things are pinned here:
//
//   - Group("/") is gin's idiomatic no-prefix group and is pervasive (ente's server
//     opens seven). Its routes must come out at /ping, not //ping — a path nothing
//     serves and no client route can match.
//   - A real prefix composes, and nests.
//   - The handler is the LAST argument, because gin takes variadic middleware first.
func main() {
	server := gin.Default()

	publicAPI := server.Group("/")
	publicAPI.GET("/ping", ping)

	privateAPI := server.Group("/")
	storageAPI := privateAPI.Group("/")
	storageAPI.GET("/files/upload-url", uploadURL)

	adminAPI := server.Group("/admin")
	adminAPI.DELETE("/user/delete", deleteUser)
	adminAPI.POST("/user/disable", rateLimit, disableUser)

	castAPI := server.Group("/cast")
	castAPI.GET("/device-info/:deviceID", deviceInfo)
	castAPI.Handle("PATCH", "/settings", patchSettings)
	castAPI.Any("/health", health)

	_ = server.Run(":8080")
}

// Every handler is func(*gin.Context) — the second HTTP handler shape in Go, tagged
// structurally like the net/http one so a route binds to the method serving it.
func ping(c *gin.Context)          {}
func uploadURL(c *gin.Context)     {}
func deleteUser(c *gin.Context)    {}
func disableUser(c *gin.Context)   {}
func deviceInfo(c *gin.Context)    {}
func patchSettings(c *gin.Context) {}
func health(c *gin.Context)        {}
func rateLimit(c *gin.Context)     {}
