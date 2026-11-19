package handler

import "github.com/gofiber/fiber/v2"

// Routes holds every HTTP handler of the API.
type Routes struct {
	Health   *HealthHandler
	Uploads  *UploadHandler
	Jobs     *JobHandler
	Releases *ReleaseHandler
	Captions *CaptionHandler
	Auth     *AuthHandler // optional: nil in route tests
	Files    *FileHandler // optional: only when files live in OneDrive
}

// RegisterRoutes mounts the whole API under /api/v1.
//
//	GET    /health                       public
//	POST   /uploads/presigned-url        auth
//	GET    /jobs, /jobs/:id              public
//	POST   /jobs                         auth
//	PUT    /jobs/:id                     auth
//	DELETE /jobs/:id                     auth   (?cleanup_files=true)
//	GET    /releases, /releases/:id      public
//	POST   /releases                     auth
//	PUT    /releases/:id                 auth
//	DELETE /releases/:id                 auth
//	POST   /releases/generate-caption    auth, then rateLimit
//	GET    /files/<key>                  public, 302 to OneDrive (when enabled)
//	/api/auth/...                         sso-balai browser SSO (see AuthHandler.Register)
//
// auth is the session middleware (AuthHandler.RequireSession). It is applied
// to mutations only; reads stay public. rateLimit runs after auth so it can
// key on the authenticated user.
func RegisterRoutes(app *fiber.App, r Routes, auth, rateLimit fiber.Handler) {
	api := app.Group("/api/v1")

	r.Health.Register(api)
	if r.Auth != nil {
		// /api/auth/*, not /api/v1: sso-balai registers Kompu's launch URL as
		// {host}/api/auth/sso/start, like SILAPET and Klinik.
		r.Auth.Register(app.Group("/api"))
	}
	if r.Files != nil {
		r.Files.Register(api)
	}
	r.Uploads.Register(api, auth)
	r.Jobs.Register(api, auth)
	r.Releases.Register(api, auth)
	r.Captions.Register(api, auth, rateLimit)
}
