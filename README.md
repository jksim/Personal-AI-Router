<!--
SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
SPDX-License-Identifier: Apache-2.0
-->

# NVIDIA Personal AI Router (PAIR)

[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
[![Security Policy](https://img.shields.io/badge/security-policy-green.svg)](SECURITY.md)

> ### This is an unofficial fork
>
> This repository is a personal fork of
> [NVIDIA/Personal-AI-Router](https://github.com/NVIDIA/Personal-AI-Router).
> It is **not affiliated with, endorsed by, or supported by NVIDIA Corporation.**
> NVIDIA, the NVIDIA logo, and NVIDIA product names are trademarks of NVIDIA
> Corporation; they are used here only to identify the upstream project this work
> derives from, and no trademark rights are claimed or granted (Apache-2.0 §6).
>
> **Do not report issues with this fork to NVIDIA.** Use this repository's issue
> tracker, and see [SECURITY.md](SECURITY.md) for vulnerability reporting.
>
> **What this fork adds.** The current release, v0.7.1, is PAIR 1.0.0 with two
> additions:
>
> - **[Modular MAX](https://www.modular.com/) as a managed engine,** beside
>   Ollama, LM Studio and llama.cpp. PAIR installs it, supervises it, lists and
>   selects its models, and routes inference to it across the cluster with the
>   same model-aware routing, failover and mTLS. Linux x86-64 only.
> - **Qualcomm Cloud AI 100 detection.** PAIR reports an AI 100 card, with its
>   memory, load, temperature and power, to every node in the cluster, beside the
>   node's GPUs. Linux only, because Qualcomm's `qaic` driver is Linux-only. PAIR
>   does not run models on the AI 100 yet; a Qualcomm engine is in development.
>
> The fork's version numbers are its own: 0.7.1 contains upstream 1.0.0. The
> release was tested on a Windows 11 node and an Ubuntu 24.04 node with an
> RTX A4500 and an AI 100 Ultra, in one cluster.
>
> Upstream code remains under its original copyright and the Apache-2.0 licence.
> Files first written in this fork carry the fork author's copyright.

NVIDIA Personal AI Router (PAIR), shown as **NVIDIA PAIR** once installed, is a
local inference router for a group of compatible computers on the same network. It discovers participating nodes,
manages supported inference engines, and presents local proxy endpoints for
Ollama-compatible, OpenAI-compatible, and Anthropic Messages API requests.
Independent requests can be routed to eligible nodes according to engine
availability, model availability, and current workload.

PAIR is useful for concurrent local workloads such as multi-agent applications.
Prompts and responses are intended to remain on the local network when every
configured client, model source, engine, and node is local.

> PAIR routes each independent request to one node. It does **not** pool GPU
> memory, combine GPUs into a larger logical GPU, shard one model across
> machines, or split an in-flight inference request between nodes.

![Two paired machines in PAIR's Overview. Requests arrive on one and are routed
across both, with each node reporting live GPU and memory use.](assets/pair-demo.gif)

*Two paired machines: requests arrive on one, run on whichever node suits each
one, and both report live GPU and memory use throughout.
[Watch the full clip](assets/pair-demo.mp4).*

## What is supported

| | |
| --- | --- |
| **Operating systems** | Windows 11; Linux; macOS |
| **Architectures** | x64 and arm64 on all three. Windows on ARM is experimental. This fork's releases are x64 only. |
| **Installers** | This fork's releases: Windows x64 `.exe` and Linux x64 `.deb`. For macOS, arm64 and other Linux distributions, [build from source](docs/building.mdx). |
| **Mixing nodes** | Windows, Linux, and macOS nodes can all be paired with each other |
| **Inference engines** | Ollama, LM Studio, llama.cpp, and Modular MAX — all installed and managed by PAIR. MAX is Linux x86-64 only (it needs AVX2 and glibc 2.34+), and PAIR installs it into its own self-contained environment rather than changing your system Python. |
| **Accelerators** | GPUs, as upstream PAIR reports them, and on Linux the Qualcomm Cloud AI 100. PAIR reports an AI 100 only after it answers a status request, so a card that failed to start is not shown. To read the card, your user needs access to `/dev/accel/*`; see the [node-info reference](services/nvpair-node-info/README.md). |

**PAIR running on a machine does not mean an engine will.** PAIR itself runs on
any supported Windows, Linux, or macOS machine. Each engine sets its own requirements
for the operating system, GPU, and drivers, and each model needs enough memory to
load. Whether a particular engine and model work on a particular machine is
between that engine and that machine, so check the engine's own documentation
before assuming a node can serve a model. A node only becomes a candidate for a
request once it is actually running a compatible engine, and PAIR prefers the
nodes it already knows hold the model.

## Quick start

Download a released build and use the desktop application. That is the path we
recommend and the one the rest of this guide assumes. Building from source and
the terminal interface both exist for good reasons — changing PAIR, and machines
with no desktop — but neither is the ordinary way in. Those are covered in
[Building and running PAIR from source](docs/building.mdx) and
[Terminal interface](docs/terminal-interface.mdx).

### Download a release

A released installer sets up the background services and the desktop
application together, and on Windows it adds the firewall rules PAIR needs and
the Microsoft Visual C++ runtime.

This fork's installers are **not code-signed**, and they check no update feed.
Watch the fork's releases page for new versions.

Download PAIR from this fork's
[releases page](https://github.com/jksim/Personal-AI-Router/releases). Each
release includes:

- `NVPAIR-Setup-<version>-x64.exe`, the Windows x64 installer;
- `NVPAIR-Setup-<version>-amd64.deb`, the Linux x64 package; and
- `SHA256SUMS.txt`. Check your download against it.

The fork uses the same app ID and data folder as official PAIR. Installing it
**replaces** an official PAIR install, and keeps its settings and cluster.

**On Windows,** run the installer.

1. SmartScreen shows "Windows protected your PC", because the installer is not
   signed. Select **More info**, then **Run anyway**.
2. User Account Control asks about an app "from an unknown publisher". Select
   **Yes**.
3. When PAIR first starts, Windows asks to allow `nvpair-ui-broker` on the
   network. Select **Allow**.

**On Linux,** install the package from the directory you downloaded it into:

```bash
sudo apt install ./NVPAIR-Setup-0.7.1-amd64.deb
```

This also upgrades an installed official PAIR package.

### Run it

- **Open PAIR** the way you would any application — the Start menu on Windows,
  Launchpad or the Applications folder on macOS, your applications list on Linux.
  On a machine with no desktop environment, drive it from the
  [terminal interface](docs/terminal-interface.mdx) instead, which starts the same
  background services and gives you a full-screen view in the terminal.

- **Let it finish starting.** **Overview** shows this machine once the services
  are up. If it stays on **Loading...**, open **Settings → Service** and read the
  status there.

- **Get an engine running.** On the node's card, open **Engine settings** and
  select **Install** next to Ollama, LM Studio, llama.cpp, or MAX (Linux x86-64). PAIR downloads and
  sets the engine up for you, so nothing needs to be in place beforehand. The
  first-start wizard preselects Ollama, LM Studio and llama.cpp, but not MAX.

  ![The Install engines dialog with Ollama downloading, reporting progress as it installs.](docs/assets/onboarding/engine-lifecycle/01-engine-installing.png)

- **Add a model.** Select **Add model** on the same card and download one.
  `qwen4:12b` is used for this example; it can be replaced with a model of your
  choice. llama.cpp populates this list from popular GGUF repositories on
  Hugging Face. Type to filter the list, or press Enter to search the wider
  public catalog. Its exact IDs include the repository and quantization, such
  as `ggml-org/gemma-3-1b-it-GGUF:Q4_K_M`.

  ![A node card with its engine expanded, one model pulling and the Add model button beside the list.](docs/assets/onboarding/getting-started/07-add-model.png)

- **Send a request.** Two equally good options:

  - **Let PAIR generate the traffic.** Select **Test** on
    **Settings → Service** and PAIR sends a minute of inference through the same
    path, so you can watch the jobs appear without writing anything.

    ![Overview during a test run, with jobs in flight across both machines in the cluster.](docs/assets/onboarding/getting-started/12-demo-traffic.png)

  - **Send one yourself.** Use the `curl` call below. The job then appears under
    **Jobs**, naming the node that served it.

With Ollama on its default port, this runs as written:

```bash
curl http://127.0.0.1:11434/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen4:12b","messages":[{"role":"user","content":"In one sentence, what does a router do?"}]}'
```

The reply is ordinary OpenAI-shaped JSON, abbreviated here:

```json
{
  "object": "chat.completion",
  "model": "qwen4:12b",
  "choices": [
    {
      "message": {
        "role": "assistant",
        "content": "A router decides where each incoming message should go and forwards it there."
      },
      "finish_reason": "stop"
    }
  ]
}
```

If you changed a port, or you are using LM Studio, llama.cpp or MAX rather than
Ollama, copy the URL from **Endpoints → API endpoints** instead of assuming the
one above.

That is a single machine working. To route across machines, pair a second one
from **Settings → Cluster** and repeat the engine and model steps there. The
inviting machine shows a six-digit PIN, and you enter that PIN on the machine you
invited.

![The six-digit pairing PIN on the inviting machine beside the Cluster invitation modal on the machine being invited.](docs/assets/onboarding/getting-started/04-pairing-pin.png)

The [Getting Started Guide](docs/getting-started.mdx) covers the same ground in
detail, plus pairing, ports, and connecting your own applications.

## Uninstalling

Removing PAIR and removing your data are separate steps, and the default is to
keep your data.

**Windows.** Uninstall from Apps & features, or the Start menu entry. The
uninstaller stops PAIR, removes its firewall rules, and asks whether to delete
your data. Decline and it stays; accept and it is removed.

**Linux.** `sudo apt remove nvpair` uninstalls the application and keeps your
data. Use `sudo apt purge nvpair` to remove the data as well. Run
`dpkg -l | grep -i pair` first if you need to confirm the installed package name.
A purge removes the engines PAIR installed under its own data directory, but not
one a vendor installer placed in your home, as LM Studio does — uninstall that
engine from PAIR before removing the package, or use its own uninstaller.

**macOS.** Run the uninstaller that ships inside the app bundle. It stops PAIR,
removes its firewall rules, unregisters its privileged helper, and then removes
the application:

```bash
sudo "/Applications/NVIDIA PAIR.app/Contents/Resources/installer-tools/uninstall-macos.sh"
```

An install from before the app was renamed is still called `PAIR.app`; use
`/Applications/PAIR.app` in the path instead. Add `--purge` to remove your data
as well. Dragging PAIR to the Trash instead
leaves the privileged helper registered, so use the uninstaller.

Your data means settings, logs, cluster identity and certificates, and any engine
PAIR installed for you. An engine the uninstaller cannot remove — one still
holding a file open, say — is left in place rather than stopping the uninstall;
remove it with its own uninstaller. **Model weights are not touched** — they live in the
engine's own storage (`~/.ollama`, `~/.llamacpp`, `~/.lmstudio/models`, and
MAX's `~/.cache/huggingface`), so
removing PAIR does not delete the models you downloaded. Delete those through the
engine, or by removing its directory. An engine you installed yourself is also
left alone.

**To clear your data without uninstalling,** use **Settings → Service → Reset app
data**. It removes the same set — settings, logs, cluster identity and
certificates, and PAIR-installed engines — then restarts the application as if it
were newly installed. Model libraries are left alone here too. This is the quickest
way to start over after a broken cluster or a bad engine install.

If this machine belongs to a cluster, deal with membership too — otherwise the
other nodes keep listing it as a member. You have two options:

- **Leave from this machine** before uninstalling: **Settings → Cluster →
  Leave**, or press `l` on the terminal interface's **Nodes** tab and confirm
  with `y`.
- **Remove it from another node**, which any member can do from
  **Settings → Cluster** by removing that node from the list.

The second option works after the fact as well, so forgetting to leave first is
recoverable.

## Documentation

If you are new to PAIR, reading in this order will get you productive fastest.
Each entry assumes the ones before it.

1. **[Overview](docs/overview.mdx)** — what PAIR does and how its pieces fit
   together. Start here so the vocabulary in every other document makes sense.
2. **[Getting started](docs/getting-started.mdx)** — install it, pair two
   machines, prepare a model, and send a first request. This is the only document
   most users need. When you update, **[Release notes](docs/release-notes.mdx)**
   lists what changed and the **[Upgrade guide](docs/upgrade-guide.mdx)** covers
   any steps the update needs.
3. **[Managing engines](docs/engine-lifecycle.mdx)** — install, start, stop,
   update, and uninstall engines; what PAIR restores after you quit or relaunch.
4. **[Engine settings](docs/engine-settings.mdx)** — change an engine's ports and
   its launch command, on this machine or a paired one, and give a browser access
   to your models.
5. **[Terminal interface](docs/terminal-interface.mdx)** — the same tasks from a
   terminal, for a machine with no desktop environment. Skip it if every machine
   you run has a desktop.
6. **[Troubleshooting](docs/troubleshooting.mdx)** — worth skimming once before
   you need it, so you know where the diagnostics live. Alongside it,
   **[Known issues](docs/known-issues.mdx)** lists the significant limitations we
   are already aware of, and
   **[Collecting and sanitizing logs](docs/log-collection.mdx)** covers preparing
   a log you can share.
7. **[Architecture](docs/architecture.mdx)** — the process model, how a request is
   routed, and where the trust boundaries are. Read this before changing
   anything, or if you want to know why PAIR behaves the way it does.
8. **[Building and running](docs/building.mdx)** — prerequisites, building from
   source, running the services without the desktop application, and writing
   your own client against the JSON-RPC API.
9. **[Developer guide](docs/developing.mdx)** — read this before contributing:
   where the code lives, how a change travels through the layers, and the
   conventions the project enforces.

Component references, for when you already know what you are looking for:

- [Services](services/readme.md) — the background services, and from there each
  component's own reference
- [Desktop application](desktop/README.md) — working in the application, and from
  there its architecture, contracts, and CLI documentation

## Releases

See this fork's [releases page](https://github.com/jksim/Personal-AI-Router/releases)
for what changed in each fork release, its known issues and what it was tested
on. Upstream's changes are in
[NVIDIA's releases](https://github.com/NVIDIA/Personal-AI-Router/releases) and
the [Release notes](docs/release-notes.mdx).

Known issues in v0.7.1:

- The terminal interface says "PAIR 1.0.0 is available". This build already
  contains 1.0.0, so ignore the notice.
- **Settings → Service** shows "Error checking for updates", because the fork's
  builds have no update feed.
- If another program uses port 8000, PAIR moves MAX's proxy to the next free
  port. Copy MAX's URL from **Endpoints → API endpoints**.

## Roadmap

This is upstream PAIR's roadmap. This fork follows upstream, and adds only what
the fork notice at the top describes. The list is NVIDIA's direction for the
project, not a commitment to delivery or implementation order. Community
feedback and contributions will help shape priorities.

### Platform support

- [ ] Full support for Windows ARM64 systems.
- [ ] DGX Station support.

### Engines and integrations

- [x] llama.cpp support.
- [ ] vLLM support.
- [ ] EXO support.
- [ ] ComfyUI integration.
- [ ] Unsloth support.
- [ ] Tailscale integration.

### Routing and clusters

- [ ] Route OpenAI-compatible API requests across different inference engines.
- [ ] Inference request queuing.
- [ ] Increase number of scheduler variables for better QoS.
- [ ] A "cluster as a node" view.
- [ ] KV cache-aware scheduling.

### Usability and reliability

- [ ] Requester-only clients that send work without hosting an inference engine.
- [ ] Launch PAIR automatically on system startup.
- [x] Configure engine launch options and environment variables from PAIR.
- [ ] Stability and reliability improvements informed by real-world use.

Feature requests for upstream PAIR go to
[NVIDIA's issues](https://github.com/NVIDIA/Personal-AI-Router/issues). Requests
for MAX or the AI 100 go to [this fork's issues](https://github.com/jksim/Personal-AI-Router/issues).

## Development Team

NVIDIA team members working on upstream PAIR. They do not maintain this fork.

| Name | GitHub | Role |
| --- | --- | --- |
| Noah Tervalon (Terve) | [@Noah-Tervalon-Nvidia](https://github.com/Noah-Tervalon-Nvidia) | PAIR Developer - Community Lead |
| Chris Kelsey | [@ckelseynv](https://github.com/ckelseynv) | PAIR Developer - UI/UX Lead |
| Sherief Farouk | [@sherief-nv](https://github.com/sherief-nv) | PAIR Developer - Scheduling, Team Lead |
| Preston Goode | [@nv-pgoode](https://github.com/nv-pgoode) | PAIR Developer - Engine Management Lead |
| Kaylee Lubick | [@kjlubick](https://github.com/kjlubick) | PAIR Developer - Security Lead |
| Lucas Brodzinski | [@LB-NV](https://github.com/lb-nv) | PAIR Technical Program Manager |
| Ambrish Dantrey | [@adantrey](https://github.com/adantrey) | PAIR Engineering Manager |
| Seth Schneider | [@NV-sschneider](https://github.com/NV-sschneider) | PAIR Product Manager |

## Contributing and governance

- [Contributing](CONTRIBUTING.md)
- [Code of Conduct](CODE_OF_CONDUCT.md)
- [Governance](GOVERNANCE.md)

## Support

See [SUPPORT.md](SUPPORT.md) for public support channels and scope.

## Security

PAIR includes local HTTP endpoints, LAN discovery, a PIN-based trust bootstrap,
and cluster networking. Read [SECURITY.md](SECURITY.md) before deploying it on
an untrusted or shared network. Do not report vulnerabilities in a public issue.

## License

This project is licensed under the [Apache License 2.0](LICENSE). See
[Third-Party Software Notices](THIRD_PARTY_NOTICES.md) for bundled dependencies.
Inference engines, models, and other software used with PAIR may have separate terms.
