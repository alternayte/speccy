package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/alternayte/speccy/internal/http/api"
)

// envModels reads SPECCY_MODELS: the model of each role, for CI where no app configures them.
// The format is "role=backend:model", separated by ";" or new lines; "all" sets every role.
// Example: "all=anthropic:claude-sonnet-5; reader_2=openai:gpt-5". The API key of a backend is
// in SPECCY_<BACKEND>_API_KEY. Speccy picks no default model (SDD §19 Q3).
func envModels() (map[string][2]string, error) {
	raw := strings.TrimSpace(os.Getenv("SPECCY_MODELS"))
	if raw == "" {
		return nil, nil
	}
	roles := []string{"reviewer", "reader_1", "reader_2", "reader_3", "judge", "writer"}
	out := map[string][2]string{}
	for _, item := range strings.FieldsFunc(raw, func(r rune) bool { return r == ';' || r == '\n' }) {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		role, spec, ok := strings.Cut(item, "=")
		kind, model, ok2 := strings.Cut(strings.TrimSpace(spec), ":")
		role, kind, model = strings.TrimSpace(role), strings.TrimSpace(kind), strings.TrimSpace(model)
		if !ok || !ok2 || model == "" {
			return nil, fmt.Errorf("SPECCY_MODELS: %q is not role=backend:model", item)
		}
		switch api.BackendKind(kind) {
		case api.Anthropic, api.Openai, api.Openrouter, api.Deepseek:
		default:
			return nil, fmt.Errorf("SPECCY_MODELS: %q is not a backend with an API key: use anthropic, openai, openrouter, or deepseek", kind)
		}
		if role == "all" {
			for _, r := range roles {
				out[r] = [2]string{kind, model}
			}
			continue
		}
		if !slices.Contains(roles, role) {
			return nil, fmt.Errorf("SPECCY_MODELS: %q is not a role: use %s, or all", role, strings.Join(roles, ", "))
		}
		out[role] = [2]string{kind, model}
	}
	return out, nil
}

// applyEnvModels makes a backend for each backend kind in SPECCY_MODELS, with its API key, and
// assigns the roles. It uses the API as the local user.
func applyEnvModels(ctx context.Context, c *api.ClientWithResponses) error {
	models, err := envModels()
	if err != nil || models == nil {
		return err
	}
	list, err := c.ListBackendsWithResponse(ctx)
	if err != nil {
		return err
	}
	if list.JSON200 == nil {
		return errors.New(problemText(list.ApplicationproblemJSONDefault))
	}
	ids := map[string]string{}
	kinds := map[string]bool{}
	for _, m := range models {
		kinds[m[0]] = true
	}
	sorted := make([]string, 0, len(kinds))
	for k := range kinds {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	for _, kind := range sorted {
		env := "SPECCY_" + strings.ToUpper(kind) + "_API_KEY"
		key := strings.TrimSpace(os.Getenv(env))
		if key == "" {
			return fmt.Errorf("SPECCY_MODELS uses %s, but %s is not set", kind, env)
		}
		name := "ci-" + kind
		body := api.BackendInput{Kind: api.BackendKind(kind), Name: name, Secret: &key}
		for _, b := range list.JSON200.Items {
			if b.Name == name {
				res, err := c.UpdateBackendWithResponse(ctx, b.Id, body)
				if err != nil {
					return err
				}
				if res.JSON200 == nil {
					return errors.New(problemText(res.ApplicationproblemJSONDefault))
				}
				ids[kind] = b.Id.String()
			}
		}
		if ids[kind] == "" {
			res, err := c.CreateBackendWithResponse(ctx, body)
			if err != nil {
				return err
			}
			if res.JSON201 == nil {
				return errors.New(problemText(res.ApplicationproblemJSONDefault))
			}
			ids[kind] = res.JSON201.Id.String()
		}
	}
	for role, m := range models {
		id, err := parseUUID(ids[m[0]])
		if err != nil {
			return err
		}
		res, err := c.AssignRoleWithResponse(ctx, api.RoleName(role), api.RoleInput{BackendId: id, Model: m[1]})
		if err != nil {
			return err
		}
		if res.StatusCode() >= 300 {
			return errors.New(problemText(res.ApplicationproblemJSONDefault))
		}
	}
	return nil
}
