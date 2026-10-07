package cli

import (
	"context"
	"fmt"

	"github.com/monolithiclab/musickit/internal/applemusic"
	"github.com/monolithiclab/musickit/internal/auth"
	"github.com/monolithiclab/musickit/internal/config"
)

// AuthCmd runs, or clears, the browser authorisation.
type AuthCmd struct {
	Reauth bool `help:"Discard the cached user token and sign in again."`
	Logout bool `help:"Discard the cached user token and stop."`
	Port   int  `default:"8888" help:"Loopback port for the authorisation page."`
}

// Run performs the authorisation.
func (c *AuthCmd) Run(ctx context.Context, rt *Runtime) error {
	if c.Logout {
		if err := auth.ForgetToken(rt.Paths.Token); err != nil {
			return err
		}
		rt.Logf("Forgot the cached user token (%s).", rt.Paths.Token)
		return nil
	}

	rt.authPort = c.Port
	rt.reauth = c.Reauth
	client, err := rt.API(ctx, true)
	if err != nil {
		return err
	}
	rt.Logf("Authorised. Storefront: %s", client.Storefront)
	rt.Row("ok", client.Storefront)
	return nil
}

// dialAPI builds the real client: load the config, sign a developer token,
// and only then — if the command needs to write — obtain a user token.
func (rt *Runtime) dialAPI(ctx context.Context, needUser bool) (*applemusic.Client, error) {
	cfg, err := config.Load(rt.Paths)
	if err != nil {
		return nil, err
	}
	devToken, err := auth.DeveloperTokenFromFile(cfg.PrivateKey, cfg.TeamID, cfg.KeyID)
	if err != nil {
		return nil, err
	}

	client := applemusic.New(devToken, rt.G.Timeout)
	client.Storefront = firstNonEmpty(rt.G.Storefront, cfg.Storefront)

	if needUser {
		token, err := rt.userToken(ctx, client, devToken)
		if err != nil {
			return nil, err
		}
		client.UserToken = token
	}
	if client.Storefront == "" {
		if needUser {
			if client.Storefront, err = client.FetchStorefront(ctx); err != nil {
				return nil, err
			}
		} else {
			client.Storefront = applemusic.DefaultStorefront
		}
	}
	return client, nil
}

// userToken returns the cached Music-User-Token or runs the browser handshake.
// Only MusicKit JS can mint one: Apple exposes no server-side grant, so this
// step cannot be removed, in any language.
func (rt *Runtime) userToken(ctx context.Context, client *applemusic.Client, devToken string) (string, error) {
	if !rt.reauth {
		if token, ok := auth.CachedToken(rt.Paths.Token); ok {
			return token, nil
		}
	}

	// Check the signing key before involving a browser: a bad key produces a
	// much clearer error here than three clicks later.
	if err := client.Ping(ctx); err != nil {
		return "", fmt.Errorf("checking the developer token: %w", err)
	}

	port := rt.authPort
	if port == 0 {
		port = auth.DefaultAuthPort
	}
	authorizer := &auth.Authorizer{
		Addr:   fmt.Sprintf("127.0.0.1:%d", port),
		Notify: func(url string) { rt.Warnf("Opening %s — click Authorise, then come back here.", url) },
	}
	token, err := authorizer.Token(ctx, devToken)
	if err != nil {
		return "", err
	}
	if err := auth.SaveToken(rt.Paths.Token, token); err != nil {
		return "", err
	}
	rt.Logf("Cached user token in %s", rt.Paths.Token)
	return token, nil
}
