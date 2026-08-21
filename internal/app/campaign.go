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

	"github.com/pelletier/go-toml/v2"
)

type campaignSource struct {
	Key            string   `json:"key" toml:"key"`
	AllowedMediums []string `json:"allowed_mediums" toml:"allowed_mediums"`
}

type campaignDefinition struct {
	Key         string `json:"key" toml:"key"`
	Label       string `json:"label" toml:"label"`
	Status      string `json:"status" toml:"status"`
	Description string `json:"description,omitempty" toml:"description"`
	ID          string `json:"id,omitempty" toml:"id"`
}

type campaignPolicy struct {
	Strict         bool                 `json:"strict" toml:"strict"`
	PolicyVersion  int                  `json:"policy_version" toml:"policy_version"`
	AllowedMediums []string             `json:"allowed_mediums" toml:"allowed_mediums"`
	Sources        []campaignSource     `json:"sources" toml:"sources"`
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
var campaignToken = regexp.MustCompile(`^[a-z0-9]+(?:[-_][a-z0-9]+)*$`)
var campaignEmail = regexp.MustCompile(`(?i)[^\s@]+@[^\s@]+\.[^\s@]+`)
var campaignPhone = regexp.MustCompile(`(?:\+?\d[\d .()\-]{7,}\d)`)

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
	if containsCampaignPII(values["label"]) || containsCampaignPII(values["description"]) {
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
	if containsCampaignPII(values["label"]) || containsCampaignPII(values["description"]) {
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

func tomlValue(stanza, key string) string {
	var values struct {
		Campaigns []campaignDefinition `toml:"campaigns"`
	}
	if err := toml.Unmarshal([]byte("[[campaigns]]"+stanza), &values); err != nil {
		return ""
	}
	if len(values.Campaigns) != 1 {
		return ""
	}
	switch key {
	case "key":
		return values.Campaigns[0].Key
	case "label":
		return values.Campaigns[0].Label
	case "status":
		return values.Campaigns[0].Status
	}
	return ""
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
	var file struct {
		Campaign  campaignPolicy       `toml:"campaign"`
		Campaigns []campaignDefinition `toml:"campaigns"`
	}
	if err := toml.Unmarshal([]byte(data), &file); err != nil {
		return campaignPolicy{}, fmt.Errorf("invalid .hs.toml: %w", err)
	}
	file.Campaign.Campaigns = file.Campaigns
	return file.Campaign, nil
}

func validateCampaignPolicy(policy campaignPolicy) error {
	if !policy.Strict || policy.PolicyVersion != 1 {
		return errors.New("campaign policy must set strict = true and policy_version = 1")
	}
	if len(policy.AllowedMediums) == 0 || len(policy.Sources) == 0 {
		return errors.New("campaign policy must configure allowed mediums and sources")
	}
	sources := map[string]bool{}
	for _, source := range policy.Sources {
		if err := validateCampaignSlug(source.Key, "source key"); err != nil {
			return err
		}
		if sources[source.Key] {
			return fmt.Errorf("campaign policy defines source %q more than once", source.Key)
		}
		sources[source.Key] = true
		for _, medium := range source.AllowedMediums {
			if !hasString(policy.AllowedMediums, medium) {
				return fmt.Errorf("source %q uses unapproved medium %q", source.Key, medium)
			}
			if !campaignToken.MatchString(medium) {
				return fmt.Errorf("source %q has invalid medium %q", source.Key, medium)
			}
		}
	}
	campaignKeys := map[string]bool{}
	for _, campaign := range policy.Campaigns {
		if err := validateCampaignSlug(campaign.Key, "campaign key"); err != nil {
			return err
		}
		if campaign.Label == "" || (campaign.Status != "active" && campaign.Status != "retired") {
			return fmt.Errorf("campaign %q must have a label and active or retired status", campaign.Key)
		}
		if campaignKeys[campaign.Key] {
			return fmt.Errorf("campaign policy defines campaign %q more than once", campaign.Key)
		}
		campaignKeys[campaign.Key] = true
		if containsCampaignPII(campaign.Label) || containsCampaignPII(campaign.Description) || containsCampaignPII(campaign.ID) {
			return fmt.Errorf("campaign %q contains personal data", campaign.Key)
		}
	}
	return nil
}

func validateCampaignSlug(value, label string) error {
	if !campaignSlug.MatchString(value) || containsCampaignPII(value) {
		return fmt.Errorf("%s must be lowercase kebab-case and contain no personal data", label)
	}
	return nil
}

func containsCampaignPII(value string) bool {
	return campaignEmail.MatchString(value) || campaignPhone.MatchString(value)
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
	if sourceKey == "google" && (medium == "cpc" || strings.HasPrefix(medium, "paid")) {
		return campaignLinkResult{}, errors.New("use Google Ads auto-tagging instead of manual Google UTM links")
	}
	for label, value := range map[string]string{"campaign": campaignKey, "source": sourceKey, "content": content} {
		if value != "" {
			if err := validateCampaignSlug(value, label); err != nil {
				return campaignLinkResult{}, err
			}
		}
	}
	if !campaignToken.MatchString(medium) || containsCampaignPII(medium) {
		return campaignLinkResult{}, errors.New("medium must be lowercase and contain no personal data")
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
	if err := validateUTMQuery(parsed.RawQuery); err != nil {
		return campaignLinkResult{}, err
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
	for label, value := range map[string]string{"campaign": campaignKey, "source": sourceKey, "content": content} {
		if value != "" {
			if err := validateCampaignSlug(value, label); err != nil {
				return campaignLinkResult{}, err
			}
		}
	}
	if !campaignToken.MatchString(medium) || containsCampaignPII(medium) {
		return campaignLinkResult{}, errors.New("medium must be lowercase and contain no personal data")
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

func validateUTMQuery(raw string) error {
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, "&") {
		if part == "" {
			continue
		}
		key, _, _ := strings.Cut(part, "=")
		decoded, err := url.QueryUnescape(key)
		if err != nil {
			return errors.New("campaign URL has an invalid query string")
		}
		if strings.HasPrefix(strings.ToLower(decoded), "utm_") {
			if seen[decoded] {
				return fmt.Errorf("campaign URL has duplicate %s", decoded)
			}
			seen[decoded] = true
		}
	}
	return nil
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
