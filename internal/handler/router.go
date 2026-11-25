package handler

import "github.com/gofiber/fiber/v2"

// Routes holds every HTTP handler of the API.
type Routes struct {
	Health   *HealthHandler
	Uploads  *UploadHandler
	Items    *ItemHandler
	Captions *CaptionHandler
	Auth     *AuthHandler // optional: nil in route tests
	Files    *FileHandler // optional: only when OneDrive is configured
}

// RegisterRoutes mounts the whole API under /api/v1.
//
//	GET    /health                       public
//	POST   /uploads/presigned-url        auth
//	GET    /files/<key>                  auth (?thumb=1 for a same-origin image)
//	GET    /channels, /holidays, /people auth
//	GET    /reports                      auth
//	*      /items...                     auth (see ItemHandler.Register)
//	POST   /captions/generate            auth, then rateLimit
//	POST   /captions/objective           auth, then rateLimit
//	/api/auth/...                        sso-balai browser SSO (see AuthHandler.Register)
//
// auth is the session middleware (AuthHandler.RequireSession). Only health and
// the sign-in routes are public: items hold transcripts and staff names.
// rateLimit runs after auth so it can key on the authenticated user.
func RegisterRoutes(app *fiber.App, r Routes, auth, rateLimit fiber.Handler) {
	api := app.Group("/api/v1")

	r.Health.Register(api)
	if r.Auth != nil {
		// /api/auth/*, not /api/v1: sso-balai registers Kompu's launch URL as
		// {host}/api/auth/sso/start, like SILAPET and Klinik.
		r.Auth.Register(app.Group("/api"))
	}
	if r.Files != nil {
		r.Files.Register(api, auth)
	}
	r.Uploads.Register(api, auth)
	r.Items.Register(api, auth)
	r.Captions.Register(api, auth, rateLimit)
}

func withMiddleware(mw []fiber.Handler, final fiber.Handler) []fiber.Handler {
	out := make([]fiber.Handler, 0, len(mw)+1)
	out = append(out, mw...)
	return append(out, final)
}
