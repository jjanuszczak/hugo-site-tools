(function(global) {
  "use strict";

  function sessionToken() {
    return new URLSearchParams(global.location.search).get("token") || "";
  }

  function request(path, options) {
    options = options || {};
    options.headers = options.headers || {};
    options.headers["X-HS-Session"] = sessionToken();
    return fetch(path, options).then(function(response) {
      return response.json().then(function(data) {
        if (!response.ok) {
          throw new Error(data.error || "Request failed.");
        }
        return data;
      });
    });
  }

  function metric(title, store, key) {
    var panel = new JOG.SectionPanel();
    var label = new JOG.Label();
    panel.Title = title;
    panel.Padding = 12;
    label.BindText(store, key);
    panel.Add(label);
    return panel;
  }

  function statusText(job) {
    if (job.status === "running") {
      return "Running " + job.kind + "...";
    }
    if (job.status === "failed") {
      return "Failed: " + job.error;
    }
    return "Completed " + job.kind + ".";
  }

  function HSWebPage() {
    JOG.Page.call(this);
    this.Title = "hs web";

    var store = new JOG.Store({
      contentCount: "-",
      draftCount: "-",
      wordCount: "-",
      sectionCount: "-",
      statusText: "Ready.",
      sourceText: "Select a content item to read its source.",
      findingText: "Run a release check to see findings.",
      filterText: "",
      campaignPolicyText: "Loading campaign policy...",
      campaignContentSource: "",
      campaignKey: "",
      campaignSource: "",
      campaignMedium: "",
      campaignContent: "",
      campaignResult: "Generate or validate a campaign link.",
      validationURL: ""
    });
    var collection = new JOG.Collection({ idKey: "source", rows: [] });

    var shell = new JOG.WorkspaceShell();
    shell.Fill = true;
    shell.Padding = 18;
    shell.SidebarMinWidth = 220;
    shell.ContentMinWidth = 520;
    shell.SidebarLayout = { base: { dock: "top", height: 150, gap: 14 }, md: { dock: "left", width: 250, height: null, gap: 18 } };

    var header = new JOG.StackPanel();
    header.Orientation = "horizontal";
    header.Gap = 18;
    var topBar = new JOG.PageHeader();
    topBar.TitleText = "Hugo Site Tools";
    topBar.SubtitleText = "Loading project...";
    topBar.Fill = true;
    var refresh = new JOG.Button();
    refresh.Text = "Refresh content";
    refresh.ThemePreset = "quiet";
    header.Add(topBar);
    header.Add(refresh);

    var dashboard = new JOG.StackPanel();
    dashboard.Orientation = "vertical";
    dashboard.Gap = 10;
    dashboard.Fill = true;
    var dashboardTitle = new JOG.Label();
    dashboardTitle.Text = "Dashboard";
    dashboardTitle.ThemePreset = "strong";
    var dashboardGrid = new JOG.Grid();
    dashboardGrid.Columns = ["1fr", "1fr", "1fr", "1fr"];
    dashboardGrid.ColumnGap = 12;
    dashboardGrid.Responsive = { base: { columns: ["1fr", "1fr"] }, md: { columns: ["1fr", "1fr", "1fr", "1fr"] } };
    dashboardGrid.Add(metric("Content", store, "contentCount"));
    dashboardGrid.Add(metric("Drafts", store, "draftCount"));
    dashboardGrid.Add(metric("Words", store, "wordCount"));
    dashboardGrid.Add(metric("Sections", store, "sectionCount"));
    dashboard.Add(dashboardTitle);
    dashboard.Add(dashboardGrid);

    var explorer = new JOG.StackPanel();
    explorer.Orientation = "vertical";
    explorer.Gap = 10;
    explorer.Fill = true;
    var explorerTitle = new JOG.Label();
    explorerTitle.Text = "Content explorer";
    explorerTitle.ThemePreset = "strong";
    var explorerLayout = new JOG.StackPanel();
    explorerLayout.Orientation = "vertical";
    explorerLayout.Gap = 10;
    explorerLayout.Fill = true;
    var filter = new JOG.TextBox();
    filter.Placeholder = "Filter title, tag, or body";
    filter.Width = 320;
    filter.BindText(store, "filterText");
    var grid = new JOG.DataGrid();
    grid.Collection = collection;
    grid.Fill = true;
    grid.FilterColumns = ["title", "section", "tags", "source"];
    grid.EmptyText = "No local content matches this filter.";
    grid.ResizableColumns = true;
    grid.Columns = [
      { key: "title", title: "Title", minWidth: 220 },
      { key: "section", title: "Section", width: "120px" },
      { key: "draft", title: "Draft", width: "80px", formatter: function(value) { return value ? "Draft" : "Published"; } },
      { key: "word_count", title: "Words", width: "90px", align: "right" },
      { key: "date", title: "Date", width: "120px", formatter: function(value) { return value ? String(value).slice(0, 10) : ""; } }
    ];
    explorerLayout.Add(explorerTitle);
    explorerLayout.Add(filter);
    explorerLayout.Add(grid);

    var inspectorTitle = new JOG.Label();
    inspectorTitle.Text = "Source preview";
    inspectorTitle.ThemePreset = "strong";
    var source = new JOG.TextArea();
    source.ReadOnly = true;
    source.Height = 160;
    source.BindText(store, "sourceText");
    explorerLayout.Add(inspectorTitle);
    explorerLayout.Add(source);
    explorer.Add(explorerLayout);

    var release = new JOG.StackPanel();
    release.Orientation = "vertical";
    release.Gap = 10;
    release.Fill = true;
    var releaseTitle = new JOG.Label();
    releaseTitle.Text = "Release workbench";
    releaseTitle.ThemePreset = "strong";
    var releaseLayout = new JOG.StackPanel();
    releaseLayout.Orientation = "vertical";
    releaseLayout.Gap = 10;
    releaseLayout.Fill = true;
    var actions = new JOG.StackPanel();
    actions.Orientation = "horizontal";
    actions.Gap = 10;
    var build = new JOG.Button(); build.Text = "Build"; build.ThemePreset = "primary";
    var doctor = new JOG.Button(); doctor.Text = "Run doctor";
    var seo = new JOG.Button(); seo.Text = "Audit SEO"; seo.ThemePreset = "quiet";
    var links = new JOG.Button(); links.Text = "Audit links"; links.ThemePreset = "quiet";
    var status = new JOG.Label(); status.BindText(store, "statusText");
    var findings = new JOG.TextArea(); findings.ReadOnly = true; findings.Height = 180; findings.BindText(store, "findingText");
    actions.Add(build); actions.Add(doctor); actions.Add(seo); actions.Add(links);
    releaseLayout.Add(releaseTitle); releaseLayout.Add(actions); releaseLayout.Add(status); releaseLayout.Add(findings);
    release.Add(releaseLayout);

    var campaigns = new JOG.StackPanel();
    campaigns.Orientation = "vertical";
    campaigns.Gap = 10;
    campaigns.Fill = true;
    var campaignsTitle = new JOG.Label();
    campaignsTitle.Text = "Campaign links";
    campaignsTitle.ThemePreset = "strong";
    var campaignPolicy = new JOG.TextArea();
    campaignPolicy.ReadOnly = true;
    campaignPolicy.Height = 130;
    campaignPolicy.BindText(store, "campaignPolicyText");
    var campaignFields = new JOG.Grid();
    campaignFields.Columns = ["150px", "1fr"];
    campaignFields.ColumnGap = 10;
    campaignFields.RowGap = 8;
    campaignFields.Responsive = { base: { columns: ["1fr"] }, md: { columns: ["150px", "1fr"] } };
    function campaignField(row, labelText, key, placeholder) {
      var label = new JOG.Label();
      var input = new JOG.TextBox();
      label.Text = labelText;
      label.GridColumn = 1;
      label.GridRow = row;
      input.GridColumn = 2;
      input.GridRow = row;
      input.Placeholder = placeholder;
      input.BindText(store, key);
      campaignFields.Add(label);
      campaignFields.Add(input);
    }
    campaignField(1, "Content source", "campaignContentSource", "content/articles/example.md");
    campaignField(2, "Campaign", "campaignKey", "approved campaign key");
    campaignField(3, "Source", "campaignSource", "linkedin");
    campaignField(4, "Medium", "campaignMedium", "social");
    campaignField(5, "Content", "campaignContent", "optional creative or placement");
    var campaignActions = new JOG.StackPanel();
    campaignActions.Orientation = "horizontal";
    campaignActions.Gap = 10;
    var generateCampaign = new JOG.Button();
    generateCampaign.Text = "Generate link";
    generateCampaign.ThemePreset = "primary";
    campaignActions.Add(generateCampaign);
    var copyCampaignLink = new JOG.Button();
    copyCampaignLink.Text = "Copy link";
    campaignActions.Add(copyCampaignLink);
    var downloadCampaignQR = new JOG.Button();
    downloadCampaignQR.Text = "Download QR";
    campaignActions.Add(downloadCampaignQR);
    var copyCampaignQR = new JOG.Button();
    copyCampaignQR.Text = "Copy QR";
    campaignActions.Add(copyCampaignQR);
    var validateCampaign = new JOG.Button();
    validateCampaign.Text = "Validate URL";
    validateCampaign.ThemePreset = "quiet";
    campaignActions.Add(validateCampaign);
    var validateURL = new JOG.TextBox();
    validateURL.Placeholder = "Paste a campaign URL to validate";
    validateURL.Width = 520;
    validateURL.BindText(store, "validationURL");
    var campaignResult = new JOG.TextArea();
    campaignResult.ReadOnly = true;
    campaignResult.Height = 150;
    campaignResult.BindText(store, "campaignResult");
    campaigns.Add(campaignsTitle);
    campaigns.Add(campaignPolicy);
    campaigns.Add(campaignFields);
    campaigns.Add(campaignActions);
    campaigns.Add(validateURL);
    campaigns.Add(campaignResult);

    var content = new JOG.TabControl();
    content.ActiveTab = "dashboard";
    content.Fill = true;
    function tab(key, title, control) {
      var page = new JOG.TabPage();
      page.TabKey = key;
      page.Title = title;
      page.Add(control);
      content.Add(page);
    }
    tab("dashboard", "Dashboard", dashboard);
    tab("content", "Content explorer", explorer);
    tab("release", "Release workbench", release);
    tab("campaigns", "Campaign links", campaigns);

    shell.Header = header;
    shell.Content = content;
    this.Add(shell);

    function loadProject() {
      return request("/api/project").then(function(data) {
        var identity = data.kind === "repository" ? data.repository + " @ " + data.commit.slice(0, 12) : data.project;
        topBar.SubtitleText = identity;
      });
    }
    function loadContent() {
      return Promise.all([request("/api/content"), request("/api/stats")]).then(function(results) {
        var rows = results[0];
        var stats = results[1];
        collection.SetRows(rows);
        collection.MarkClean();
        store.Set("contentCount", String(rows.length));
        store.Set("draftCount", String(rows.filter(function(row) { return row.draft; }).length));
        store.Set("wordCount", String(stats.total_words || 0));
        store.Set("sectionCount", String(Object.keys(stats.sections || {}).length));
        store.Set("statusText", "Content refreshed.");
      });
    }
    function loadCampaignPolicy() {
      return request("/api/campaigns").then(function(policy) {
        var campaigns = policy.campaigns || [];
        store.Set("campaignPolicyText", "Policy v" + policy.policy_version + "\nSources: " + (policy.sources || []).map(function(source) { return source.key + " (" + source.allowed_mediums.join(", ") + ")"; }).join("\n") + "\nCampaigns: " + (campaigns.length ? campaigns.map(function(campaign) { return campaign.key + " [" + campaign.status + "]"; }).join("\n") : "None configured"));
      }).catch(function(error) { store.Set("campaignPolicyText", "Campaign links are unavailable: " + error.message); });
    }
    function campaignResultText(result) {
      return "URL: " + result.url + "\nCampaign: " + result.campaign + "\nMedium: " + result.medium + "\nExpected GA4 channel: " + result.expected_ga4_channel;
    }
    function campaignURL() {
      return store.Get("validationURL") || "";
    }
    function fetchCampaignQR() {
      return fetch("/api/campaign-links/qr", { method: "POST", headers: { "Content-Type": "application/json", "X-HS-Session": sessionToken() }, body: JSON.stringify({ url: campaignURL() }) }).then(function(response) {
        if (!response.ok) {
          return response.json().then(function(data) { throw new Error(data.error || "Could not generate QR code."); });
        }
        return response.blob();
      });
    }
    function runJob(kind) {
      store.Set("statusText", "Starting " + kind + "...");
      request("/api/jobs", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ kind: kind }) }).then(function(job) {
        function poll() {
          request("/api/jobs/" + job.id).then(function(current) {
            store.Set("statusText", statusText(current));
            if (current.status === "running") {
              global.setTimeout(poll, 700);
              return;
            }
            if (current.result && current.result.findings) {
              store.Set("findingText", current.result.findings.map(function(finding) {
                return String(finding.severity || "info").toUpperCase() + " " + finding.code + " " + finding.source + (finding.line ? ":" + finding.line : "") + "\n" + finding.message;
              }).join("\n\n") || "No findings.");
            } else if (current.result) {
              store.Set("findingText", JSON.stringify(current.result, null, 2));
            }
          }).catch(function(error) { store.Set("statusText", error.message); });
        }
        poll();
      }).catch(function(error) { store.Set("statusText", error.message); });
    }

    refresh.OnClick(function() { loadContent().catch(function(error) { store.Set("statusText", error.message); }); });
    store.Subscribe("filterText", function(value) { grid.FilterText = value; });
    grid.OnSelectionChange(function(args) {
      if (!args.Row) { return; }
      request("/api/source?path=" + encodeURIComponent(args.Row.source)).then(function(data) {
        store.Set("sourceText", data.text);
        store.Set("campaignContentSource", args.Row.source);
      }).catch(function(error) { store.Set("sourceText", error.message); });
    });
    build.OnClick(function() { runJob("build"); });
    doctor.OnClick(function() { runJob("doctor"); });
    seo.OnClick(function() { runJob("seo"); });
    links.OnClick(function() { runJob("links"); });
    generateCampaign.OnClick(function() {
      request("/api/campaign-links", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ content_source: store.Get("campaignContentSource"), campaign: store.Get("campaignKey"), source: store.Get("campaignSource"), medium: store.Get("campaignMedium"), content: store.Get("campaignContent") }) }).then(function(result) {
        store.Set("campaignResult", campaignResultText(result));
        store.Set("validationURL", result.url);
      }).catch(function(error) { store.Set("campaignResult", "Cannot generate link: " + error.message); });
    });
    copyCampaignLink.OnClick(function() {
      var url = campaignURL();
      if (!url) { store.Set("campaignResult", "Generate or validate a campaign link first."); return; }
      if (!global.navigator.clipboard || !global.navigator.clipboard.writeText) {
        store.Set("campaignResult", "This browser does not support copying links to the clipboard.");
        return;
      }
      global.navigator.clipboard.writeText(url).then(function() {
        store.Set("campaignResult", "Copied campaign link to clipboard.\n\nURL: " + url);
      }).catch(function(error) { store.Set("campaignResult", "Cannot copy link: " + error.message); });
    });
    downloadCampaignQR.OnClick(function() {
      if (!campaignURL()) { store.Set("campaignResult", "Generate or validate a campaign link first."); return; }
      fetchCampaignQR().then(function(blob) {
        var link = global.document.createElement("a");
        link.href = global.URL.createObjectURL(blob);
        link.download = "campaign-qr.png";
        link.click();
        global.setTimeout(function() { global.URL.revokeObjectURL(link.href); }, 1000);
        store.Set("campaignResult", "Downloaded campaign QR code.");
      }).catch(function(error) { store.Set("campaignResult", "Cannot download QR code: " + error.message); });
    });
    copyCampaignQR.OnClick(function() {
      if (!campaignURL()) { store.Set("campaignResult", "Generate or validate a campaign link first."); return; }
      if (!global.ClipboardItem || !global.navigator.clipboard || !global.navigator.clipboard.write) {
        store.Set("campaignResult", "This browser does not support copying images to the clipboard.");
        return;
      }
      fetchCampaignQR().then(function(blob) {
        return global.navigator.clipboard.write([new global.ClipboardItem({ "image/png": blob })]);
      }).then(function() { store.Set("campaignResult", "Copied campaign QR code to clipboard."); }).catch(function(error) { store.Set("campaignResult", "Cannot copy QR code: " + error.message); });
    });
    validateCampaign.OnClick(function() {
      request("/api/campaign-links/validate", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ url: store.Get("validationURL") }) }).then(function(result) {
        store.Set("campaignResult", "Valid campaign link\n" + campaignResultText(result));
      }).catch(function(error) { store.Set("campaignResult", "Invalid campaign link: " + error.message); });
    });

    Promise.all([loadProject(), loadContent(), loadCampaignPolicy()]).catch(function(error) { store.Set("statusText", error.message); });
  }

  HSWebPage.prototype = Object.create(JOG.Page.prototype);
  HSWebPage.prototype.constructor = HSWebPage;

  global.addEventListener("load", function() {
    var app = new JOG.Application();
    app.Theme = { colors: { primary: "#0f766e", primaryText: "#f0fdfa", appBackground: "#f7faf9" } };
    app.Run(new HSWebPage());
  }, false);
})(window);
