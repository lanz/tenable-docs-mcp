# With and without the Tenable Docs MCP

Four questions from the server's [evaluation sets](../internal/search/testdata), asked of the same assistant twice: once on its own, and once with the Tenable Docs MCP and its [companion skill](../skills/tenable-docs). The table compares each answer with what the docs say; the answers themselves follow, exactly as given.

**How it was run** (November 9, 2026): Claude, through Claude Code in headless mode (`claude -p`), from an empty folder, with web search, web fetching, file access, and subagents turned off in both runs, so the only difference is the server. Each answer is from a single run.

| Question | What the docs say | Without the server | With the server |
| --- | --- | --- | --- |
| Which ports does Nessus need open? | TCP 8834 inbound (the interface, Tenable Security Center, and the API); TCP 25, TCP 443, and UDP 53 outbound; and, for agents, TCP 443, TCP 8834, and UDP 53. [Port Requirements](https://docs.tenable.com/nessus/Content/PortRequirements.htm#TenableNessus) | Gets the core right (8834 inbound; 443 and 25 outbound), and adds ports the docs don't list, such as 465 and 587 for email and 8080 and 3128 for a proxy. No citations. | Matches the Port Requirements page, for the scanner and for agents, with links to both sections. |
| What's the default maximum number of hosts Nessus scans at the same time? | 30: **Max simultaneous hosts per scan**, capped by the scanner's `max_hosts`. [Advanced Scan Settings](https://docs.tenable.com/nessus/Content/AdvancedScanSettings.htm#ScannerTemplateAdvancedSettings) | Right number, but calls the setting "Max Concurrent Hosts", and adds a "Max Hosts Per Scan" limit of 1,024 or 4,096 that the docs don't describe. | 30, with the setting's name, how it interacts with the scanner's `max_hosts` (256 by default), and links to both pages. |
| What role do I need to generate API keys in Tenable Vulnerability Management? | Basic, Scan Operator, Standard, Scan Manager, or Administrator, for your own keys; Administrator for another user's. [Generate API Keys](https://docs.tenable.com/vulnerability-management/Content/Settings/my-account/GenerateAPIKey.htm) | **Wrong:** says Administrator by default, with an "API Key Generation" toggle for other users that the docs don't describe. | Correct, with the roles as the page states them, the Administrator-only case, and links to both pages. |
| Where do I see my cloud sensors in Tenable Vulnerability Management? | **Sensors** in the left navigation, then **Nessus Scanners**, then **Cloud Scanners**; Scan Manager or Administrator role. [View Sensors and Sensor Groups](https://docs.tenable.com/vulnerability-management/Content/Settings/Sensors/ViewSensors.htm) | **Wrong path** (Settings > Sensors > Cloud Sensors), mixed up with cloud connectors and a deployment flow the page doesn't describe. | Correct path and role, notes the difference from cloud connectors, and links to both pages. |

Without the server, the assistant answers from what it remembers: often close, sometimes wrong in ways that are hard to spot (a toggle or a menu path that doesn't exist), and never with a link to check. With it, each answer comes from the current docs, says which product and version it's for, and links to the exact section.

## The answers

### 1. Which ports does Nessus need open?

<details>
<summary>Without the server</summary>

## Nessus port requirements

Nessus is mostly a **client** — it opens outbound connections and only listens on one port itself.

### Inbound to the Nessus scanner / Nessus Manager
| Port | Protocol | Purpose |
|------|----------|---------|
| **8834** | TCP | Nessus web UI and REST API (users, browsers, automation). This is the only port Nessus listens on by default; it's set at install time and can be changed. |

### Outbound from the scanner / manager
| Port | Protocol | Destination | Purpose |
|------|----------|-------------|---------|
| **8834** | TCP | Nessus Manager | Linked scanners and Nessus Agents registering with an on‑prem Nessus Manager |
| **443** | TCP | `plugins.nessus.org`, `download.nessus.org`, `cloud.tenable.com`, `sensors.cloud.tenable.com` | Plugin updates, license activation, Tenable Vulnerability Management (Tenable.io) connectivity |
| **25 / 465 / 587** | TCP | Your SMTP relay | Optional — email notifications |
| **8080 / 3128** | TCP | Proxy | Optional — if the scanner reaches the internet through a proxy |

If the environment is air‑gapped, plugin updates can be done offline (download the plugin archive elsewhere and upload it), so 443 isn't strictly required.

### Outbound to scan targets (driven by the scan policy)
The scanner connects out to whatever the policy asks for; no ports need to be opened *inbound* on the scanner for this. Typical requirements:

- **Full port scan:** TCP 1–65535 and UDP 1–65535 (Nessus's `all` port range).
- **Host discovery only:** ICMP echo, plus a small default set such as TCP 21, 22, 23, 25, 80, 111, 135, 139, 443, 445, 515, 631, 1025, 1900, 2401, 3389, 5900, 6000, 8080 and UDP 137, 161.
- **Credentialed scans** also need:
  - Windows: TCP 445 (SMB), 135 + dynamic RPC (1024–65535, often 49152–65535), 5985/5986 (WinRM)
  - Linux/Unix: TCP 22 (SSH)
  - Network devices: UDP 161 (SNMP)
  - VMware: TCP 443, 902 (if applicable)

### Key takeaways
1. Only **TCP 8834 inbound** is required on the Nessus host itself.
2. **TCP 443 outbound** is needed for plugin/license updates and Tenable cloud connectivity.
3. **TCP 8834 outbound** is needed only for scanner/agent → on‑prem Nessus Manager linking.
4. All target-facing traffic is outbound and dictated by the scan policy — plan firewall rules from the scanner toward the scanned subnets rather than inbound to the scanner.

</details>

<details>
<summary>With the Tenable Docs MCP</summary>

Here's what the current **Nessus User Guide** (Welcome › System Requirements › Port Requirements) lists. It applies to Nessus Manager, Professional, Expert, Essentials, and scanners.

**Inbound (to the Nessus instance)** — [Port Requirements › Tenable Nessus](https://docs.tenable.com/nessus/Content/PortRequirements.htm#TenableNessus)

| Port | Purpose |
| --- | --- |
| **TCP 8834** | Accessing the Tenable Nessus interface, communicating with Tenable Security Center, and interacting with the API |

**Outbound**

| Port | Purpose |
| --- | --- |
| **TCP 25** | Sending SMTP email notifications |
| **TCP 443** | Communicating with Tenable One Vulnerability Management (`sensor.cloud.tenable.com` or `sensor.cloud.tenablecloud.cn`) and with `plugins.nessus.org` for plugin updates |
| **UDP 53** | Performing DNS resolution |

**Nessus Agents** — outbound only ([Port Requirements › Tenable Nessus Agents](https://docs.tenable.com/nessus/Content/PortRequirements.htm#TenableNessusAgents))

| Port | Purpose |
| --- | --- |
| **TCP 443** | Communicating with Tenable One Vulnerability Management |
| **TCP 8834** | Communicating with Tenable Nessus Manager (the default port; it's configurable and may differ in your organization) |
| **UDP 53** | External DNS support for the agent host; several plugins use DNS resolution |

The docs add a note that OS installation commands such as `dnf install` may need other connections beyond those, so check with your OS administrator. The above covers the Nessus host and agents — target-side ports opened for scanning are a separate matter, driven by each scan's port scan settings.

</details>

### 2. What's the default maximum number of hosts Nessus scans at the same time?

<details>
<summary>Without the server</summary>

**30 hosts at a time.** That's the default value of **Max Concurrent Hosts** in a scan's Performance settings (Advanced settings).

Two related defaults worth distinguishing:

- **Max Concurrent Checks Per Host:** 5 (plugins running simultaneously against one host)
- **Max Hosts Per Scan:** up to 1,024 (total hosts in the scan target list — a separate limit from concurrency, and historically up to 4,096 in some editions/versions)

You can raise Max Concurrent Hosts in the policy settings, but the practical ceiling depends on your Nessus license/edition and the scanner's resources and bandwidth — pushing it too high often makes scans slower rather than faster due to congestion.

</details>

<details>
<summary>With the Tenable Docs MCP</summary>

**30 hosts** — for a scan's **Max simultaneous hosts per scan** setting in Nessus (current version, English docs).

There are actually three limits that interact, so the answer depends on which one you mean:

| Setting | Where | Default |
|---|---|---|
| **Max simultaneous hosts per scan** | Scan settings: Advanced Scan Settings › Performance | **30** (or the scanner's `max_hosts`, whichever is smaller) |
| **Max Concurrent Hosts Per Scan** (`max_hosts`) | Scanner: Advanced Settings › Performance | **256** (setting it to 0 also yields 256) |
| **Global Max Hosts Concurrently Scanned** (`global.max_hosts`) | Scanner: Advanced Settings › Performance | Varies depending on hardware |

So a scan out of the box scans up to 30 hosts at once. You can raise the per-scan value above 30, but Nessus caps it at the scanner's `max_hosts` — e.g., set the scan to 150 with `max_hosts` at 100 and it will only run 100 at a time.

Sources:
- [Advanced Scan Settings › Scanner Template Advanced Settings](https://docs.tenable.com/nessus/Content/AdvancedScanSettings.htm#ScannerTemplateAdvancedSettings)
- [Advanced Settings › Performance](https://docs.tenable.com/nessus/Content/SettingsAdvanced.htm#Performance) — requires the **System Administrator** role when using Nessus Manager

</details>

### 3. What role do I need to generate API keys in Tenable Vulnerability Management?

<details>
<summary>Without the server</summary>

**Administrator**, by default — but it's really a per-user permission that admins control.

Details:

- **Administrators** can generate API keys for their own account right away (Settings → My Account → API Keys), and can also generate them on behalf of other users.
- **Non-administrator users** can generate their own keys only if an Administrator enables the **API Key Generation** setting on their user account (Settings → Users → select the user → API Key Generation toggle). Once enabled, that user creates their keys under Settings → My Account → API Keys.
- A key is tied to the user who created it and **inherits that user's role and permissions** — so a Standard user's key can only do what a Standard user can do. Tenable generally recommends a dedicated account (often with Administrator role) for integrations so keys don't break when a person leaves.

If you want an integration key with broad access, use an Administrator account; if you want least privilege, have an admin flip the API Key Generation toggle on a role-appropriate account and generate the key as that user.

</details>

<details>
<summary>With the Tenable Docs MCP</summary>

**Required User Role:** `Basic`, `Scan Operator`, `Standard`, `Scan Manager`, or `Administrator` (the docs also list the legacy role names `Basic User`, `Standard User`).
Source: [Generate API Keys — Tenable Vulnerability Management User Guide](https://docs.tenable.com/vulnerability-management/Content/Settings/my-account/GenerateAPIKey.htm)

Any of those roles can generate API keys **for their own account** (My Account → **API Keys** tab → **Generate**). Only an **Administrator** can generate API keys **for another user's account** — see [Generate a User's API Key](https://docs.tenable.com/vulnerability-management/Content/Settings/access-control/manage-users.htm#GenerateaUsersAPIKey).

Two practical notes from the same page:
- Clicking **Generate** **replaces** any existing keys, so the applications using the old keys must be updated.
- If the **Generate** button is inactive, an administrator needs to enable API access for your account in the user account settings.

This is from the English User Guide for Tenable Vulnerability Management (Tenable One Vulnerability Management), page last updated 2026-11-06. If your question is about the API endpoints themselves rather than the UI, the [Tenable Developer Portal](https://developer.tenable.com/) is the authoritative reference.

</details>

### 4. Where do I see my cloud sensors in Tenable Vulnerability Management?

<details>
<summary>Without the server</summary>

In Tenable Vulnerability Management, cloud sensors live under the Sensors area of Settings:

**Settings → Sensors → Cloud Sensors**

From there you can see each deployed cloud sensor, its connected cloud account/region, connection status and last check‑in, and the sensor version. Cloud sensor deployment is launched from the same page (the Terraform/CloudFormation template flow), and the underlying cloud account link is set up separately under **Settings → Connectors → Cloud** (AWS / Azure / GCP). Note that many deployments name this the "Sensors" page and group Scanners / Agents / Cloud Sensors as tabs or sections, so look for a Sensors entry in the settings menu and then the Cloud Sensors section within it. Administrator-level permissions are typically needed to view or deploy them.

Two caveats: I'm answering from knowledge of the product UI rather than looking it up live (I have no web access in this session), and Tenable renames/regroups settings pages fairly often — so if "Cloud Sensors" isn't a top-level entry, check inside **Settings → Sensors** tabs, and confirm against the "Cloud Sensors" article in the Tenable docs for your release. If you tell me what you see in the settings menu, I can point you to the exact tab.

</details>

<details>
<summary>With the Tenable Docs MCP</summary>

In **Tenable Vulnerability Management**, cloud sensors (Tenable's regional cloud scanners) are on the **Sensors** page:

1. In the left navigation, click **Sensors**.
2. In the left navigation pane of the **Sensors** page, under **Nessus Scanners**, select **Cloud Scanners**.

The **Sensors** page opens with the **Nessus Scanners** tab active and **Linked Scanners** selected by default, so switch to **Cloud Scanners** to see them. That page lists the regional cloud scanner groups Tenable provides (for example **US East Cloud Scanners**, **EU Frankfurt Cloud Scanners**, **AP Sydney Cloud Scanners**), which you can select when you create and launch scans.

Required role: **Scan Manager** or **Administrator**.

One clarification on wording: **Cloud Sensors** in this sense are Tenable-managed scanners and cannot be removed from your environment. If you meant the connectors that pull asset data from AWS/Azure/GCP, those are **cloud connectors**, under Settings › Cloud Connectors — tell me if that's what you were after.

Sources (Tenable Vulnerability Management User Guide, English, current version):
- [View Sensors and Sensor Groups](https://docs.tenable.com/vulnerability-management/Content/Settings/Sensors/ViewSensors.htm)
- [Cloud Sensors](https://docs.tenable.com/vulnerability-management/Content/Settings/Sensors/CloudSensors.htm#CloudSensors)

</details>
