/*
Copyright 2022 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	cehttp "github.com/cloudevents/sdk-go/v2/protocol/http"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/go-github/v43/github"
	"github.com/kelseyhightower/envconfig"
	"golang.org/x/oauth2"
	"knative.dev/pkg/ptr"

	"chainguard.dev/api/pkg/events"
	"chainguard.dev/api/pkg/events/policy"
)

type envConfig struct {
	Issuer      string `envconfig:"ISSUER_URL" required:"true"`
	Group       string `envconfig:"GROUP" required:"true"`
	Port        int    `envconfig:"PORT" default:"8080" required:"true"`
	GithubOrg   string `envconfig:"GITHUB_ORG" required:"true"`
	GithubRepo  string `envconfig:"GITHUB_Repo" required:"true"`
	GithubToken string `envconfig:"GITHUB_TOKEN" required:"true"`
}

func main() {
	var env envConfig
	if err := envconfig.Process("", &env); err != nil {
		log.Fatalf("failed to process env var: %s", err)
	}
	c, err := cloudevents.NewClientHTTP(cloudevents.WithPort(env.Port),
		// We need to infuse the request onto context, so we can
		// authenticate requests.
		cehttp.WithRequestDataAtContextMiddleware())
	if err != nil {
		log.Fatalf("failed to create client, %v", err)
	}
	ctx := context.Background()

	// Construct a verifier that ensures tokens are issued by the Chainguard
	// issuer we expect and are intended for a customer webhook.
	provider, err := oidc.NewProvider(ctx, env.Issuer)
	if err != nil {
		log.Fatalf("failed to create provider: %v", err)
	}
	verifier := provider.Verifier(&oidc.Config{
		ClientID: "customer",
	})

	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: env.GithubToken},
	)
	tc := oauth2.NewClient(ctx, ts)

	client := github.NewClient(tc)

	receiver := func(ctx context.Context, event cloudevents.Event) error {
		// We expect Chainguard webhooks to pass an Authorization header.
		auth := strings.TrimPrefix(cehttp.RequestDataFromContext(ctx).Header.Get("Authorization"), "Bearer ")
		if auth == "" {
			return cloudevents.NewHTTPResult(http.StatusUnauthorized, "Unauthorized")
		}

		// Verify that the token is well-formed, and in fact intended for us!
		if tok, err := verifier.Verify(ctx, auth); err != nil {
			return cloudevents.NewHTTPResult(http.StatusForbidden, "unable to verify token: %w", err)
		} else if !strings.HasPrefix(tok.Subject, "webhook:") {
			return cloudevents.NewHTTPResult(http.StatusForbidden, "subject should be from the Chainguard webhook component, got: %s", tok.Subject)
		} else if group := strings.TrimPrefix(tok.Subject, "webhook:"); group != env.Group {
			return cloudevents.NewHTTPResult(http.StatusForbidden, "this token is intended for %s, wanted one for %s", group, env.Group)
		}

		// We are handling a specific event type, so filter the rest.
		if event.Type() != policy.ChangedEventType {
			return nil
		}

		body := &policy.ImagePolicyRecord{}
		data := events.Occurrence{
			Body: body,
		}
		if err := event.DataAs(&data); err != nil {
			return cloudevents.NewHTTPResult(http.StatusInternalServerError, "unable to unmarshal data: %w", err)
		}

		for name, pol := range body.Policies {
			if pol.Valid {
				// Not in violation of policy
				continue
			}
			switch pol.Change {
			case policy.ImprovedChange:
				// TODO: How is this an improvement?
				continue
			case policy.NewChange, policy.DegradedChange:
				// We want to fire on these events.
			}
			issue, _, err := client.Issues.Create(ctx, env.GithubOrg, env.GithubRepo, &github.IssueRequest{
				Title: ptr.String(fmt.Sprintf("Policy %s failed", name)),
				Body:  ptr.String(fmt.Sprintf("Image: `%s`\nCluster `%s`\nPolicy: `%s`\nLast Checked: `%v`", body.ImageID, body.ClusterID, name, pol.LastChecked.Time)),
			})
			if err != nil {
				return cloudevents.NewHTTPResult(http.StatusInternalServerError, "unable to create GitHub issue: %w", err)
			}
			log.Printf("Opened issue: %d", issue.GetNumber())
		}

		return nil
	}

	if err := c.StartReceiver(ctx, func(ctx context.Context, event cloudevents.Event) error {
		// This thunk simply wraps the main receiver in one that logs any errors
		// we encounter.
		err := receiver(ctx, event)
		if err != nil {
			log.Printf("SAW: %v", err)
		}
		return err
	}); err != nil {
		log.Fatal(err)
	}
}
