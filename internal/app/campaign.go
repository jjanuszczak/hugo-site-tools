package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type campaignSource struct {
	Key            string   `json:"key"`
	AllowedMediums []string `json:"allowed_mediums"`
}

type campaignDefinition struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Status      string `json:"status"`
	Description string `json:"description,omitempty"`
	ID          string `json:"id,omitempty"`
}

type campaignPolicy struct {
	Strict         bool                 `json:"strict"`
	PolicyVersion  int                  `json:"policy_version"`
	AllowedMediums []string             `json:"allowed_mediums"`
	Sources        []campaignSource     `json:"sources"`
	Campaigns      []campaignDefinition `json:"campaigns"`
}

type campaignLinkResult struct {
	URL             string `json:"url"`
	Source          string `json:"source"`
	GeneratedPath   string `json:"generated_path"`
	Campaign        string `json:"campaign"`
	Medium          string `json:"medium"`
	Content         string `json:"content,omitempty"`
	CampaignID      string `json:"campaign_id,omitempty"`
	ExpectedChannel string `json:"expected_ga4_channel"`
	PolicyVersion   int    `json:"policy_version"`
}

var campaignSlug = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
var campaignEmail = regexp.MustCompile(`(?i)[^\s@]+@[^\s@]+\.[^\s@]+`)

const campaignDefaultConfig = `# Strict GA4 campaign-link policy managed by hs.
[campaign]
strict = true
policy_version = 1
allowed_mediums = ["email", "social", "paid_social", "cpc"]

[[campaign.sources]]
key = "newsletter"
allowed_mediums = ["email"]

[[campaign.sources]]
key = "x"
allowed_mediums = ["social", "paid_social"]

[[campaign.sources]]
key = "linkedin"
allowed_mediums = ["social", "paid_social"]

[[campaign.sources]]
key = "messenger"
allowed_mediums = ["social", "paid_social"]
`

func runCampaign(args []string, out io.Writer) error {
	if len(args) == 0 || isHelp(args[0]) {
		fmt.Fprintln(out, "Usage: hs campaign init [project-directory]\n       hs campaign list [project-directory] [--format text|json]\n       hs campaign add <key> [project-directory] --label LABEL --description DESCRIPTION [--id ID]\n       hs campaign edit <key> [project-directory] --label LABEL --description DESCRIPTION\n       hs campaign retire <key> [project-directory]\n       hs campaign link <content-file> [project-directory] --campaign KEY --source KEY --medium MEDIUM [--content KEY] [--format text|json]\n       hs campaign validate <url> [project-directory] [--format text|json]")
		return nil
	}
	switch args[0] {
	case "init":
		return runCampaignInit(args[1:], out)
	case "list":
		return runCampaignList(args[1:], out)
	case "add":
		return runCampaignAdd(args[1:], out)
	case "edit":
		return runCampaignEdit(args[1:], out)
	case "retire":
		return runCampaignRetire(args[1:], out)
	case "link":
		return runCampaignLink(args[1:], out)
	case "validate":
		return runCampaignValidate(args[1:], out)
	default:
		return fmt.Errorf("unknown campaign command %q", args[0])
	}
}

func campaignProject(args []string) (string, []string, error) { return resolveContentProject(args) }

func runCampaignInit(args []string, out io.Writer) error {
	project, extra, err := campaignProject(args)
	if err != nil || len(extra) != 0 {
		return campaignUsage("usage: hs campaign init [project-directory]")
	}
	file := filepath.Join(project, ".hs.toml")
	existing, err := os.ReadFile(file)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if strings.Contains(string(existing), "[campaign]") || strings.Contains(string(existing), "[[campaign.sources]]") {
		return fmt.Errorf("%s already contains a campaign policy", file)
	}
	data := append(existing, []byte("\n"+campaignDefaultConfig)...)
	if err := os.WriteFile(file, data, 0644); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Created %s\n", file)
	return err
}

func runCampaignList(args []string, out io.Writer) error {
	format, positional, err := campaignFormat(args)
	if err != nil {
		return err
	}
	project, extra, err := campaignProject(positional)
	if err != nil || len(extra) != 0 {
		return campaignUsage("usage: hs campaign list [project-directory] [--format text|json]")
	}
	policy, err := loadCampaignPolicy(project)
	if err != nil {
		return err
	}
	if format == "json" {
		return json.NewEncoder(out).Encode(policy)
	}
	for _, campaign := range policy.Campaigns {
		fmt.Fprintf(out, "%s\t%s\t%s\n", campaign.Key, campaign.Status, campaign.Label)
	}
	return nil
}

func runCampaignAdd(args []string, out io.Writer) error {
	values, positional, err := campaignValues(args, map[string]bool{"label": true, "description": true, "id": true})
	if err != nil {
		return err
	}
	if len(positional) == 0 || len(positional) > 2 || values["label"] == "" || values["description"] == "" {
		return campaignUsage("usage: hs campaign add <key> [project-directory] --label LABEL --description DESCRIPTION [--id ID]")
	}
	key := positional[0]
	if err := validateCampaignSlug(key, "campaign key"); err != nil {
		return err
	}
	project := ""
	if len(positional) == 2 {
		project = positional[1]
	}
	if project == "" {
		project, err = os.Getwd()
		if err != nil {
			return err
		}
	}
	policy, err := loadCampaignPolicy(project)
	if err != nil {
		return err
	}
	if findCampaign(policy, key) != nil {
		return fmt.Errorf("campaign %q already exists", key)
	}
	if campaignEmail.MatchString(values["label"]) || campaignEmail.MatchString(values["description"]) {
		return errors.New("campaign labels and descriptions must not contain personal data")
	}
	if values["id"] != "" && strings.ContainsAny(values["id"], " \t\n") {
		return errors.New("--id must not contain whitespace")
	}
	file := filepath.Join(project, ".hs.toml")
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "\n[[campaigns]]\nkey = %q\nlabel = %q\nstatus = \"active\"\ndescription = %q\n", key, values["label"], values["description"])
	if err == nil && values["id"] != "" {
		_, err = fmt.Fprintf(f, "id = %q\n", values["id"])
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Added campaign %s\n", key)
	return err
}

func runCampaignRetire(args []string, out io.Writer) error {
	if len(args) == 0 {
		return campaignUsage("usage: hs campaign retire <key> [project-directory]")
	}
	project, extra, err := campaignProject(args[1:])
	if err != nil || len(extra) != 0 {
		return campaignUsage("usage: hs campaign retire <key> [project-directory]")
	}
	key := args[0]
	policy, err := loadCampaignPolicy(project)
	if err != nil {
		return err
	}
	campaign := findCampaign(policy, key)
	if campaign == nil {
		return fmt.Errorf("campaign %q is not configured", key)
	}
	if campaign.Status == "retired" {
		return fmt.Errorf("campaign %q is already retired", key)
	}
	file := filepath.Join(project, ".hs.toml")
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	stanzas := strings.Split(string(data), "[[campaigns]]")
	found := false
	for i := 1; i < len(stanzas); i++ {
		if tomlValue(stanzas[i], "key") == key {
			stanzas[i] = regexp.MustCompile(`(?m)^status\s*=\s*"[^"]*"\s*$`).ReplaceAllString(stanzas[i], `status = "retired"`)
			found = true
		}
	}
	if !found {
		return fmt.Errorf("campaign %q could not be updated safely", key)
	}
	if err := os.WriteFile(file, []byte(strings.Join(stanzas, "[[campaigns]]")), 0644); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Retired campaign %s\n", key)
	return err
}

func runCampaignEdit(args []string, out io.Writer) error {
	values, positional, err := campaignValues(args, map[string]bool{"label": true, "description": true})
	if err != nil {
		return err
	}
	if len(positional) == 0 || len(positional) > 2 || values["label"] == "" || values["description"] == "" {
		return campaignUsage("usage: hs campaign edit <key> [project-directory] --label LABEL --description DESCRIPTION")
	}
	key := positional[0]
	if err := validateCampaignSlug(key, "campaign key"); err != nil {
		return err
	}
	project := ""
	if len(positional) == 2 {
		project = positional[1]
	}
	if project == "" {
		project, err = os.Getwd()
		if err != nil {
			return err
		}
	}
	if campaignEmail.MatchString(values["label"]) || campaignEmail.MatchString(values["description"]) {
		return errors.New("campaign labels and descriptions must not contain personal data")
	}
	policy, err := loadCampaignPolicy(project)
	if err != nil {
		return err
	}
	if findCampaign(policy, key) == nil {
		return fmt.Errorf("campaign %q is not configured", key)
	}
	if err := rewriteCampaignStanza(project, key, map[string]string{"label": values["label"], "description": values["description"]}); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Updated campaign %s\n", key)
	return err
}

func rewriteCampaignStanza(project, key string, updates map[string]string) error {
	file := filepath.Join(project, ".hs.toml")
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	stanzas := strings.Split(string(data), "[[campaigns]]")
	found := false
	for i := 1; i < len(stanzas); i++ {
		if tomlValue(stanzas[i], "key") != key {
			continue
		}
		for field, value := range updates {
			pattern := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(field) + `\s*=\s*"[^"]*"\s*$`)
			replacement := fmt.Sprintf("%s = %q", field, value)
			if pattern.MatchString(stanzas[i]) {
				stanzas[i] = pattern.ReplaceAllString(stanzas[i], replacement)
			} else {
				stanzas[i] = strings.TrimRight(stanzas[i], "\n") + "\n" + replacement + "\n"
			}
		}
		found = true
	}
	if !found {
		return fmt.Errorf("campaign %q could not be updated safely", key)
	}
	return os.WriteFile(file, []byte(strings.Join(stanzas, "[[campaigns]]")), 0644)
}

func runCampaignLink(args []string, out io.Writer) error {
	values, positional, err := campaignValues(args, map[string]bool{"campaign": true, "source": true, "medium": true, "content": true, "format": true})
	if err != nil {
		return err
	}
	if len(positional) == 0 || len(positional) > 2 || values["campaign"] == "" || values["source"] == "" || values["medium"] == "" {
		return campaignUsage("usage: hs campaign link <content-file> [project-directory] --campaign KEY --source KEY --medium MEDIUM [--content KEY] [--format text|json]")
	}
	format := firstNonEmpty(values["format"], "text")
	if format != "text" && format != "json" {
		return errors.New("--format requires text or json")
	}
	project := ""
	if len(positional) == 2 {
		project = positional[1]
	}
	if project == "" {
		project, err = os.Getwd()
		if err != nil {
			return err
		}
	}
	result, err := createCampaignLink(project, positional[0], values["campaign"], values["source"], values["medium"], values["content"])
	if err != nil {
		return err
	}
	if format == "json" {
		return json.NewEncoder(out).Encode(result)
	}
	_, err = fmt.Fprintln(out, result.URL)
	return err
}

func runCampaignValidate(args []string, out io.Writer) error {
	format, positional, err := campaignFormat(args)
	if err != nil {
		return err
	}
	if len(positional) == 0 || len(positional) > 2 {
		return campaignUsage("usage: hs campaign validate <url> [project-directory] [--format text|json]")
	}
	project := ""
	if len(positional) == 2 {
		project = positional[1]
	}
	if project == "" {
		project, err = os.Getwd()
		if err != nil {
			return err
		}
	}
	result, err := validateCampaignURL(project, positional[0])
	if err != nil {
		return err
	}
	if format == "json" {
		return json.NewEncoder(out).Encode(result)
	}
	_, err = fmt.Fprintln(out, "Valid campaign link:", result.URL)
	return err
}

func campaignUsage(message string) error { return &exitError{code: 2, message: message} }

func campaignFormat(args []string) (string, []string, error) {
	values, positional, err := campaignValues(args, map[string]bool{"format": true})
	if err != nil {
		return "", nil, err
	}
	format := firstNonEmpty(values["format"], "text")
	if format != "text" && format != "json" {
		return "", nil, errors.New("--format requires text or json")
	}
	return format, positional, nil
}

func campaignValues(args []string, allowed map[string]bool) (map[string]string, []string, error) {
	values := map[string]string{}
	var positional []string
	for i := 0; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "--") {
			positional = append(positional, args[i])
			continue
		}
		key := strings.TrimPrefix(args[i], "--")
		if !allowed[key] {
			return nil, nil, fmt.Errorf("unknown campaign option %q", args[i])
		}
		if i+1 == len(args) {
			return nil, nil, fmt.Errorf("%s requires a value", args[i])
		}
		i++
		values[key] = args[i]
	}
	return values, positional, nil
}

func loadCampaignPolicy(project string) (campaignPolicy, error) {
	data, err := os.ReadFile(filepath.Join(project, ".hs.toml"))
	if errors.Is(err, os.ErrNotExist) {
		return campaignPolicy{}, errors.New("campaign policy is missing; run hs campaign init")
	}
	if err != nil {
		return campaignPolicy{}, err
	}
	policy, err := parseCampaignPolicy(string(data))
	if err != nil {
		return campaignPolicy{}, err
	}
	return policy, validateCampaignPolicy(policy)
}

func parseCampaignPolicy(data string) (campaignPolicy, error) {
	policy := campaignPolicy{}
	section := ""
	var source *campaignSource
	var campaign *campaignDefinition
	for _, raw := range strings.Split(data, "\n") {
		line := strings.TrimSpace(strings.SplitN(raw, "#", 2)[0])
		if line == "" {
			continue
		}
		switch line {
		case "[campaign]":
			section, source, campaign = "campaign", nil, nil
			continue
		case "[[campaign.sources]]":
			section, source, campaign = "source", &campaignSource{}, nil
			policy.Sources = append(policy.Sources, *source)
			source = &policy.Sources[len(policy.Sources)-1]
			continue
		case "[[campaigns]]":
			section, source, campaign = "campaigns", nil, &campaignDefinition{}
			policy.Campaigns = append(policy.Campaigns, *campaign)
			campaign = &policy.Campaigns[len(policy.Campaigns)-1]
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		switch section {
		case "campaign":
			switch key {
			case "strict":
				policy.Strict = value == "true"
			case "policy_version":
				fmt.Sscan(value, &policy.PolicyVersion)
			case "allowed_mediums":
				policy.AllowedMediums = tomlStrings(value)
			}
		case "source":
			if source == nil {
				continue
			}
			if key == "key" {
				source.Key = tomlString(value)
			} else if key == "allowed_mediums" {
				source.AllowedMediums = tomlStrings(value)
			}
		case "campaigns":
			if campaign == nil {
				continue
			}
			switch key {
			case "key":
				campaign.Key = tomlString(value)
			case "label":
				campaign.Label = tomlString(value)
			case "status":
				campaign.Status = tomlString(value)
			case "description":
				campaign.Description = tomlString(value)
			case "id":
				campaign.ID = tomlString(value)
			}
		}
	}
	return policy, nil
}

func tomlString(value string) string { return strings.Trim(strings.TrimSpace(value), "\"") }
func tomlStrings(value string) []string {
	value = strings.Trim(strings.TrimSpace(value), "[]")
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	for i := range parts {
		parts[i] = tomlString(parts[i])
	}
	return parts
}
func tomlValue(stanza, key string) string {
	for _, line := range strings.Split(stanza, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && strings.TrimSpace(k) == key {
			return tomlString(v)
		}
	}
	return ""
}

func validateCampaignPolicy(policy campaignPolicy) error {
	if !policy.Strict || policy.PolicyVersion != 1 {
		return errors.New("campaign policy must set strict = true and policy_version = 1")
	}
	if len(policy.AllowedMediums) == 0 || len(policy.Sources) == 0 {
		return errors.New("campaign policy must configure allowed mediums and sources")
	}
	for _, source := range policy.Sources {
		if err := validateCampaignSlug(source.Key, "source key"); err != nil {
			return err
		}
		for _, medium := range source.AllowedMediums {
			if !hasString(policy.AllowedMediums, medium) {
				return fmt.Errorf("source %q uses unapproved medium %q", source.Key, medium)
			}
		}
	}
	for _, campaign := range policy.Campaigns {
		if err := validateCampaignSlug(campaign.Key, "campaign key"); err != nil {
			return err
		}
		if campaign.Label == "" || (campaign.Status != "active" && campaign.Status != "retired") {
			return fmt.Errorf("campaign %q must have a label and active or retired status", campaign.Key)
		}
	}
	return nil
}

func validateCampaignSlug(value, label string) error {
	if !campaignSlug.MatchString(value) || campaignEmail.MatchString(value) {
		return fmt.Errorf("%s must be lowercase kebab-case and contain no personal data", label)
	}
	return nil
}
func findCampaign(policy campaignPolicy, key string) *campaignDefinition {
	for i := range policy.Campaigns {
		if policy.Campaigns[i].Key == key {
			return &policy.Campaigns[i]
		}
	}
	return nil
}
func findCampaignSource(policy campaignPolicy, key string) *campaignSource {
	for i := range policy.Sources {
		if policy.Sources[i].Key == key {
			return &policy.Sources[i]
		}
	}
	return nil
}

func createCampaignLink(project, contentFile, campaignKey, sourceKey, medium, content string) (campaignLinkResult, error) {
	policy, err := loadCampaignPolicy(project)
	if err != nil {
		return campaignLinkResult{}, err
	}
	campaign := findCampaign(policy, campaignKey)
	if campaign == nil {
		return campaignLinkResult{}, fmt.Errorf("campaign %q is not configured", campaignKey)
	}
	if campaign.Status != "active" {
		return campaignLinkResult{}, fmt.Errorf("campaign %q is retired", campaignKey)
	}
	source := findCampaignSource(policy, sourceKey)
	if source == nil || !hasString(source.AllowedMediums, medium) {
		return campaignLinkResult{}, fmt.Errorf("source/medium pair %q/%q is not approved", sourceKey, medium)
	}
	for label, value := range map[string]string{"campaign": campaignKey, "source": sourceKey, "medium": medium, "content": content} {
		if value != "" {
			if err := validateCampaignSlug(value, label); err != nil {
				return campaignLinkResult{}, err
			}
		}
	}
	item, err := campaignContentItem(project, contentFile)
	if err != nil {
		return campaignLinkResult{}, err
	}
	base, err := campaignBaseURL(project)
	if err != nil {
		return campaignLinkResult{}, err
	}
	destination := base.ResolveReference(&url.URL{Path: strings.TrimLeft(item.GeneratedURL, "/")})
	query := destination.Query()
	query.Set("utm_source", sourceKey)
	query.Set("utm_medium", medium)
	query.Set("utm_campaign", campaignKey)
	if content != "" {
		query.Set("utm_content", content)
	}
	if campaign.ID != "" {
		query.Set("utm_id", campaign.ID)
	}
	destination.RawQuery = query.Encode()
	return campaignLinkResult{URL: destination.String(), Source: item.Source, GeneratedPath: item.GeneratedURL, Campaign: campaignKey, Medium: medium, Content: content, CampaignID: campaign.ID, ExpectedChannel: expectedGA4Channel(sourceKey, medium), PolicyVersion: policy.PolicyVersion}, nil
}

func campaignContentItem(project, source string) (contentItem, error) {
	items, err := collectContent(project)
	if err != nil {
		return contentItem{}, err
	}
	abs, _ := filepath.Abs(source)
	for _, item := range items {
		itemAbs, _ := filepath.Abs(filepath.Join(project, item.Source))
		if source == item.Source || abs == itemAbs {
			return item, nil
		}
	}
	return contentItem{}, fmt.Errorf("content source was not found: %s", source)
}

func campaignBaseURL(project string) (*url.URL, error) {
	files, err := hugoConfigFiles(project)
	if err != nil {
		return nil, err
	}
	raw := doctorBaseURL(files)
	if raw == "" {
		return nil, errors.New("Hugo baseURL is required to create campaign links")
	}
	base, err := url.Parse(raw)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, errors.New("Hugo baseURL must be an absolute HTTP URL")
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return nil, errors.New("Hugo baseURL must use HTTP or HTTPS")
	}
	if !strings.HasSuffix(base.Path, "/") {
		base.Path += "/"
	}
	return base, nil
}

func validateCampaignURL(project, raw string) (campaignLinkResult, error) {
	policy, err := loadCampaignPolicy(project)
	if err != nil {
		return campaignLinkResult{}, err
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return campaignLinkResult{}, errors.New("campaign URL must be absolute")
	}
	base, err := campaignBaseURL(project)
	if err != nil {
		return campaignLinkResult{}, err
	}
	if parsed.Scheme != base.Scheme || parsed.Host != base.Host || !strings.HasPrefix(parsed.Path, base.Path) {
		return campaignLinkResult{}, errors.New("campaign URL must stay within the configured Hugo baseURL")
	}
	query := parsed.Query()
	for _, key := range []string{"utm_source", "utm_medium", "utm_campaign"} {
		if query.Get(key) == "" {
			return campaignLinkResult{}, fmt.Errorf("campaign URL is missing %s", key)
		}
	}
	sourceKey, medium, campaignKey, content := query.Get("utm_source"), query.Get("utm_medium"), query.Get("utm_campaign"), query.Get("utm_content")
	if sourceKey == "google" && (medium == "cpc" || strings.HasPrefix(medium, "paid")) {
		return campaignLinkResult{}, errors.New("use Google Ads auto-tagging instead of manual Google UTM links")
	}
	for label, value := range map[string]string{"campaign": campaignKey, "source": sourceKey, "medium": medium, "content": content} {
		if value != "" {
			if err := validateCampaignSlug(value, label); err != nil {
				return campaignLinkResult{}, err
			}
		}
	}
	source := findCampaignSource(policy, sourceKey)
	if source == nil || !hasString(source.AllowedMediums, medium) {
		return campaignLinkResult{}, fmt.Errorf("source/medium pair %q/%q is not approved", sourceKey, medium)
	}
	campaign := findCampaign(policy, campaignKey)
	if campaign == nil {
		return campaignLinkResult{}, fmt.Errorf("campaign %q is not configured", campaignKey)
	}
	return campaignLinkResult{URL: parsed.String(), Campaign: campaignKey, Medium: medium, Content: content, CampaignID: query.Get("utm_id"), ExpectedChannel: expectedGA4Channel(sourceKey, medium), PolicyVersion: policy.PolicyVersion}, nil
}

func expectedGA4Channel(source, medium string) string {
	if medium == "email" {
		return "Email"
	}
	if medium == "social" {
		return "Organic Social"
	}
	if medium == "paid_social" {
		return "Paid Social"
	}
	if medium == "cpc" {
		return "Paid Search"
	}
	return "Unassigned"
}
