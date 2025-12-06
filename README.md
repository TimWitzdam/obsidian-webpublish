<div align="center">
    <h1 align="center">Obsidian WebPublish</a></h1>
    <p align="center">Share single Obsidian documents instantly.</p>
    <br />
</div>

https://github.com/user-attachments/assets/ad4d0411-6207-4612-8fd0-5f89d993abb0

## What it isn't

Obsidian WebPublish **is not** designed to publish your complete Obsidian vault as a website.

## What it is

It's rather made to quickly share single documents, which can't be updated.

## Features

- Share single Obsidian documents with one click
- Easily self-hostable using Docker
- Protection using rate limits, api keys, sanatization and size limits
- Small Docker image (~30 mb)
- Lightweight and fast, using Go

## 🚀 Deploy Obsidian WebPublish for yourself

### Single run command

1. Create a `config.yaml` file from the [config.example.yaml](web/config.example.yaml) to fit your needs.
2. Run the docker run command:

```bash
docker run -d --restart=unless-stopped -p 8080:8080 -v ./config.yaml:/config.yaml -v ./data:/data --name Obsidian-WebPublish timwitzdam/obsidian-webpublish:latest
```

### Docker compose

1. Create a `config.yaml` file from the [config.example.yaml](web/config.example.yaml) to fit your needs.
2. Create `docker-compose.yml` file and start the service

```yaml
services:
  obsidian-webpublish:
    image: timwitzdam/obsidian-webpublish:latest
    ports:
      - "8080:8080"
    volumes:
      - ./config.yaml:/config.yaml
      - ./data:/data
    restart: unless-stopped
```

## Plugin installation

The plugin is not yet listed on the community plugins of Obsidian, which means you have to install it manually:

1. Download the latest `main.js` and `manifest.json` from [the releases](https://github.com/TimWitzdam/obsidian-webpublish/releases)
2. Change directory to where your vault is stored and then into `.obsidian/plugins` (you might need to enable "Show hidden files")
3. Create a new directory for the plugin: `mkdir obsidian-webpublish`
4. Paste `main.js` and `manifest.json` into the newly created directory

## 👀 Any questions, suggestions or problems?

You're welcome to contribute to Obsidian WebPublish or open an issue.

Feel free to submit feature ideas in [GitHub Discussions](https://github.com/TimWitzdam/obsidian-webpublish/discussions)

I'm also available via mail: [contact@witzdam.com](mailto:contact@witzdam.com)

Disclaimer: This software is provided “as is” and is used or deployed at your own risk. No warranty is given, and—where legally permissible—all liability of the authors and contributors is excluded. Read more in [DISCLAIMER.md](DISCLAIMER.md)
