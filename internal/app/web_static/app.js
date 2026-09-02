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

    JOG.RegisterStyleBlock("HS.WebDataGrid", ".jog-data-grid.hs-content-grid { overflow-y: auto; overflow-x: auto; height: min(40vh, 420px) !important; min-height: 260px; } .jog-data-grid.hs-content-grid:focus { outline: 2px solid var(--jog-primary); outline-offset: 2px; }");

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
      draftRatio: "-",
      publishingCadence: "-",
      latestPublished: "-",
      sectionSummary: "Loading section distribution...",
      issueSummary: "No release checks run yet.",
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
      validationURL: "",
      lastJobId: ""
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
    dashboardGrid.Add(metric("Draft ratio", store, "draftRatio"));
    dashboardGrid.Add(metric("Publishing cadence", store, "publishingCadence"));
    dashboardGrid.Add(metric("Latest published", store, "latestPublished"));
    dashboardGrid.Add(metric("Release issues", store, "issueSummary"));
    var distributionMetric = new JOG.DropDownList();
    distributionMetric.Options = [{ value: "pages", text: "Pages" }, { value: "words", text: "Words" }];
    distributionMetric.SelectedValue = "pages";
    var sectionChart = new ChartJOG.BarChart();
    sectionChart.Horizontal = true;
    sectionChart.LabelField = "label";
    sectionChart.ValueField = "pages";
    sectionChart.TitleText = "Sections ranked by page count";
    sectionChart.Height = 300;
    dashboard.Add(dashboardTitle);
    dashboard.Add(dashboardGrid);
    dashboard.Add(distributionMetric);
    dashboard.Add(sectionChart);

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
    grid.Height = 320;
    grid.OnAttached = function() {
      this._domNode.classList.add("hs-content-grid");
      this._domNode.tabIndex = 0;
      this._domNode.setAttribute("aria-label", "Content pages");
    };
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
    source.Fill = true;
    source.MinHeight = 220;
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
    var downloadJSON = new JOG.Button(); downloadJSON.Text = "Download JSON"; downloadJSON.ThemePreset = "quiet";
    var downloadSARIF = new JOG.Button(); downloadSARIF.Text = "Download SARIF"; downloadSARIF.ThemePreset = "quiet";
    var status = new JOG.Label(); status.BindText(store, "statusText");
    var findings = new JOG.TextArea(); findings.ReadOnly = true; findings.Fill = true; findings.MinHeight = 240; findings.BindText(store, "findingText");
    actions.Add(build); actions.Add(doctor); actions.Add(seo); actions.Add(links); actions.Add(downloadJSON); actions.Add(downloadSARIF);
    releaseLayout.Add(releaseTitle); releaseLayout.Add(actions); releaseLayout.Add(status); releaseLayout.Add(findings);
    release.Add(releaseLayout);

    var campaigns = new JOG.StackPanel();
    campaigns.Orientation = "vertical";
    campaigns.Gap = 10;
    campaigns.Fill = true;
    var campaignsTitle = new JOG.Label();
    campaignsTitle.Text = "Campaign links";
    campaignsTitle.ThemePreset = "strong";
    var campaignPolicyButton = new JOG.Button();
    campaignPolicyButton.Text = "View campaign policy";
    campaignPolicyButton.ThemePreset = "quiet";
    var campaignPolicyDialog = new JOG.Dialog();
    campaignPolicyDialog.Name = "campaignPolicyDialog";
    campaignPolicyDialog.Title = "Campaign policy";
    campaignPolicyDialog.CloseButtonText = "Close";
    campaignPolicyDialog.Resizable = true;
    campaignPolicyDialog.SetBounds(180, 100, 620, 480);
    campaignPolicyDialog.MinWidth = 320;
    campaignPolicyDialog.MinHeight = 240;
    campaignPolicyDialog.Hide();
    var campaignPolicyDialogText = new JOG.TextArea();
    campaignPolicyDialogText.ReadOnly = true;
    campaignPolicyDialogText.Fill = true;
    campaignPolicyDialogText.BindText(store, "campaignPolicyText");
    campaignPolicyDialog.Add(campaignPolicyDialogText);
    var campaignFields = new JOG.Grid();
    campaignFields.Columns = ["150px", "1fr"];
    campaignFields.ColumnGap = 10;
    campaignFields.RowGap = 8;
    campaignFields.Responsive = { base: { columns: ["1fr"] }, md: { columns: ["150px", "1fr"] } };
    var campaignPolicyData = { campaigns: [], sources: [], allowed_mediums: [] };
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
    function campaignSelectField(row, labelText, key) {
      var label = new JOG.Label();
      var input = new JOG.DropDownList();
      label.Text = labelText;
      label.GridColumn = 1;
      label.GridRow = row;
      input.GridColumn = 2;
      input.GridRow = row;
      input.Options = [];
      input.BindSelectedValue(store, key);
      campaignFields.Add(label);
      campaignFields.Add(input);
      return input;
    }
    campaignField(1, "Content source", "campaignContentSource", "content/articles/example.md");
    var campaignKeySelect = campaignSelectField(2, "Campaign", "campaignKey");
    var campaignSourceSelect = campaignSelectField(3, "Source", "campaignSource");
    var campaignMediumSelect = campaignSelectField(4, "Medium", "campaignMedium");
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
    var validateURLRow = new JOG.StackPanel();
    validateURLRow.Orientation = "horizontal";
    validateURLRow.Gap = 8;
    validateURLRow.Height = 40;
    var copyValidationURL = new JOG.Button();
    copyValidationURL.Text = "⧉";
    copyValidationURL.ThemePreset = "quiet";
    copyValidationURL.Width = 44;
    validateURLRow.Add(validateURL);
    validateURLRow.Add(copyValidationURL);
    var campaignResult = new JOG.TextArea();
    campaignResult.ReadOnly = true;
    campaignResult.Height = 150;
    campaignResult.BindText(store, "campaignResult");
    campaigns.Add(campaignsTitle);
    campaigns.Add(campaignPolicyButton);
    campaigns.Add(campaignFields);
    campaigns.Add(campaignActions);
    campaigns.Add(validateURLRow);
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
    this.Add(campaignPolicyDialog);

    function centerCampaignPolicyDialog() {
      var viewportWidth = global.innerWidth || 1024;
      var viewportHeight = global.innerHeight || 768;
      var width = Math.max(320, Math.min(720, viewportWidth - 32));
      var height = Math.max(240, Math.min(620, viewportHeight - 32));
      campaignPolicyDialog.SetBounds(Math.max(16, Math.round((viewportWidth - width) / 2)), Math.max(16, Math.round((viewportHeight - height) / 2)), width, height);
    }

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
        store.Set("draftRatio", rows.length ? Math.round((rows.filter(function(row) { return row.draft; }).length / rows.length) * 100) + "%" : "0%");
        store.Set("publishingCadence", stats.average_time_between_posts || "Not enough dated pages");
        store.Set("latestPublished", stats.latest_published || "No published date");
        var sections = Object.keys(stats.sections || {}).map(function(section) {
          return { label: section || "Top-level pages", pages: stats.sections[section].posts || 0, words: stats.sections[section].words || 0 };
        }).sort(function(a, b) { return b.pages - a.pages; });
        sectionChart.Items = sections;
        store.Set("statusText", "Content refreshed.");
      });
    }
    function loadCampaignPolicy() {
      return request("/api/campaigns").then(function(policy) {
        campaignPolicyData = policy;
        campaignKeySelect.Options = (policy.campaigns || []).filter(function(campaign) { return campaign.status === "active"; }).map(function(campaign) {
          return { value: campaign.key, text: campaign.label ? campaign.label + " (" + campaign.key + ")" : campaign.key };
        });
        campaignSourceSelect.Options = (policy.sources || []).map(function(source) { return { value: source.key, text: source.key }; });
        updateCampaignMediumOptions(store.Get("campaignSource"));
        var campaigns = policy.campaigns || [];
        store.Set("campaignPolicyText", "Policy v" + policy.policy_version + "\nSources: " + (policy.sources || []).map(function(source) { return source.key + " (" + source.allowed_mediums.join(", ") + ")"; }).join("\n") + "\nCampaigns: " + (campaigns.length ? campaigns.map(function(campaign) { return campaign.key + " [" + campaign.status + "]"; }).join("\n") : "None configured"));
      }).catch(function(error) { store.Set("campaignPolicyText", "Campaign links are unavailable: " + error.message); });
    }
    function updateCampaignMediumOptions(sourceKey) {
      var source = (campaignPolicyData.sources || []).filter(function(item) { return item.key === sourceKey; })[0];
      var mediums = source ? source.allowed_mediums || [] : campaignPolicyData.allowed_mediums || [];
      campaignMediumSelect.Options = mediums.map(function(medium) { return { value: medium, text: medium }; });
      if (mediums.indexOf(store.Get("campaignMedium")) < 0) {
        store.Set("campaignMedium", mediums[0] || "");
      }
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
        store.Set("lastJobId", job.id);
        function poll() {
          request("/api/jobs/" + job.id).then(function(current) {
            store.Set("statusText", statusText(current));
            if (current.status === "running") {
              global.setTimeout(poll, 700);
              return;
            }
            if (current.result && current.result.findings) {
              var counts = { error: 0, warning: 0, info: 0 };
              current.result.findings.forEach(function(finding) { counts[String(finding.severity || "info")] = (counts[String(finding.severity || "info")] || 0) + 1; });
              store.Set("issueSummary", counts.error + " errors, " + counts.warning + " warnings, " + counts.info + " info");
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

    function downloadReport(format) {
      var jobID = store.Get("lastJobId");
      if (!jobID) { store.Set("statusText", "Run a release check first."); return; }
      request("/api/jobs/" + jobID + "/report?format=" + format).then(function(report) {
        var blob = new Blob([JSON.stringify(report, null, 2)], { type: "application/json" });
        var link = global.document.createElement("a");
        link.href = global.URL.createObjectURL(blob);
        link.download = "hs-report." + (format === "sarif" ? "sarif.json" : "json");
        link.click();
        global.setTimeout(function() { global.URL.revokeObjectURL(link.href); }, 1000);
        store.Set("statusText", "Downloaded " + format.toUpperCase() + " report.");
      }).catch(function(error) { store.Set("statusText", error.message); });
    }

    refresh.OnClick(function() { loadContent().catch(function(error) { store.Set("statusText", error.message); }); });
    store.Subscribe("filterText", function(value) { grid.FilterText = value; });
    distributionMetric.OnChange(function(args) {
      var metric = args.Value === "words" ? "words" : "pages";
      sectionChart.ValueField = metric;
      sectionChart.TitleText = "Sections ranked by " + (metric === "words" ? "word count" : "page count");
    });
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
    downloadJSON.OnClick(function() { downloadReport("json"); });
    downloadSARIF.OnClick(function() { downloadReport("sarif"); });
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
    copyValidationURL.OnClick(function() {
      var url = store.Get("validationURL");
      if (!url) { store.Set("campaignResult", "Paste or generate a campaign URL first."); return; }
      if (!global.navigator.clipboard || !global.navigator.clipboard.writeText) {
        store.Set("campaignResult", "This browser does not support copying links to the clipboard.");
        return;
      }
      global.navigator.clipboard.writeText(url).then(function() {
        store.Set("campaignResult", "Copied campaign URL to clipboard.\n\nURL: " + url);
      }).catch(function(error) { store.Set("campaignResult", "Cannot copy URL: " + error.message); });
    });
    campaignSourceSelect.OnChange(function(args) { updateCampaignMediumOptions(args.Value); });
    campaignPolicyButton.OnClick(function() {
      centerCampaignPolicyDialog();
      campaignPolicyDialog.ShowModal();
    });
    global.addEventListener("resize", function() {
      if (campaignPolicyDialog.Visible) { centerCampaignPolicyDialog(); }
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
