package handler

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"

	"kompu-backend/internal/model"
	"kompu-backend/internal/service"
	"kompu-backend/pkg/middleware"
)

// SSOAuth is the browser-SSO flow the handler needs (service.SSOService).
type SSOAuth interface {
	Start(ctx context.Context) (string, error)
	LogoutURL(cancelReturn string) (string, error)
	Complete(ctx context.Context, state, code, status string) (*service.Login, error)
	Authenticate(ctx context.Context, cookie string) (*model.Member, error)
	Refresh(ctx context.Context, cookie string) (*service.Login, error)
}

// AuthHandler serves the sso-balai browser-SSO routes and the session
// middleware. Tokens and the Central sid stay inside the encrypted HttpOnly
// cookie; responses only ever carry the member.
type AuthHandler struct {
	sso    SSOAuth
	secure bool
	ttl    time.Duration
}

func NewAuthHandler(sso SSOAuth, cookieSecure bool, ttl time.Duration) *AuthHandler {
	return &AuthHandler{sso: sso, secure: cookieSecure, ttl: ttl}
}

// Register mounts:
//
//	GET  /auth/sso/start      302 to sso-balai /authorize (also the portal launch URL)
//	POST /auth/sso/exchange   {code?, state, status?} -> cookie + {user} | {status:"pending"}
//	GET  /auth/sso/logout     302 to sso-balai /logout (?cancel_return=/path)
//	GET  /auth/me             204 without cookie, else {user}
//	POST /auth/refresh        rotate tokens -> cookie + {user}
//	POST /auth/logout         {centralLogoutRequired:true}; the cookie is kept until Central decides
func (h *AuthHandler) Register(r fiber.Router) {
	g := r.Group("/auth", noStore)
	g.Get("/sso/start", h.start)
	g.Post("/sso/exchange", h.exchange)
	g.Get("/sso/logout", h.logoutStart)
	g.Get("/me", h.me)
	g.Post("/refresh", h.refresh)
	g.Post("/logout", h.RequireSession, h.logout)
}

// RequireSession authenticates the request from the session cookie and stores
// the member as the request Principal.
func (h *AuthHandler) RequireSession(c *fiber.Ctx) error {
	m, err := h.sso.Authenticate(c.UserContext(), c.Cookies(service.SessionCookieName))
	if err != nil {
		return h.fail(c, err)
	}
	middleware.SetPrincipal(c, &middleware.Principal{UserID: m.ID, Role: m.Role})
	return c.Next()
}

func (h *AuthHandler) start(c *fiber.Ctx) error {
	u, err := h.sso.Start(c.UserContext())
	if err != nil {
		return err
	}
	return c.Redirect(u, fiber.StatusFound)
}

func (h *AuthHandler) logoutStart(c *fiber.Ctx) error {
	h.clearCookie(c)
	u, err := h.sso.LogoutURL(c.Query("cancel_return"))
	if err != nil {
		return err
	}
	return c.Redirect(u, fiber.StatusFound)
}

func (h *AuthHandler) exchange(c *fiber.Ctx) error {
	var req struct {
		Code   string `json:"code"`
		State  string `json:"state"`
		Status string `json:"status"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}
	login, err := h.sso.Complete(c.UserContext(), req.State, req.Code, req.Status)
	if err != nil {
		return h.fail(c, err)
	}
	if login.Pending {
		return c.JSON(fiber.Map{"status": model.MemberPending})
	}
	h.setCookie(c, login.Cookie)
	return c.JSON(fiber.Map{"user": login.Member})
}

func (h *AuthHandler) me(c *fiber.Ctx) error {
	if c.Cookies(service.SessionCookieName) == "" {
		return c.SendStatus(fiber.StatusNoContent) // quiet probe for signed-out browsers
	}
	m, err := h.sso.Authenticate(c.UserContext(), c.Cookies(service.SessionCookieName))
	if err != nil {
		return h.fail(c, err)
	}
	return c.JSON(fiber.Map{"user": m})
}

func (h *AuthHandler) refresh(c *fiber.Ctx) error {
	login, err := h.sso.Refresh(c.UserContext(), c.Cookies(service.SessionCookieName))
	if err != nil {
		return h.fail(c, err)
	}
	h.setCookie(c, login.Cookie)
	return c.JSON(fiber.Map{"user": login.Member})
}

func (h *AuthHandler) logout(c *fiber.Ctx) error {
	h.clearCookie(c)
	return c.JSON(fiber.Map{"centralLogoutRequired": true})
}

func (h *AuthHandler) fail(c *fiber.Ctx, err error) error {
	if ae, ok := err.(*service.AuthError); ok && ae.ClearSession {
		h.clearCookie(c)
	}
	return err
}

func (h *AuthHandler) setCookie(c *fiber.Ctx, value string) {
	c.Cookie(&fiber.Cookie{
		Name:     service.SessionCookieName,
		Value:    value,
		Path:     "/",
		HTTPOnly: true,
		Secure:   h.secure,
		SameSite: fiber.CookieSameSiteLaxMode,
		MaxAge:   int(h.ttl.Seconds()),
	})
}

func (h *AuthHandler) clearCookie(c *fiber.Ctx) {
	c.Cookie(&fiber.Cookie{
		Name:     service.SessionCookieName,
		Value:    "",
		Path:     "/",
		HTTPOnly: true,
		Secure:   h.secure,
		SameSite: fiber.CookieSameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
	})
}

func noStore(c *fiber.Ctx) error {
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.Next()
}
