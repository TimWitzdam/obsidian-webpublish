import {
	App,
	MarkdownView,
	Notice,
	Plugin,
	PluginSettingTab,
	Setting,
	TFile,
	requestUrl,
} from "obsidian";

interface WebPublishSettings {
	apiBaseUrl: string;
	apiKey: string;
}

const DEFAULT_SETTINGS: WebPublishSettings = {
	apiBaseUrl: "http://localhost:8080",
	apiKey: "",
};

const FRONTMATTER_KEY = "webpublish-url";
const IMAGE_EXTENSIONS = new Set([
	"png",
	"jpg",
	"jpeg",
	"gif",
	"webp",
	"bmp",
	"svg",
	"tif",
	"tiff",
]);

type UploadResponse = {
	id: string;
	url: string;
};

export default class WebPublishPlugin extends Plugin {
	settings: WebPublishSettings;

	async onload() {
		await this.loadSettings();
		this.addCommand({
			id: "webpublish-upload-current-note",
			name: "WebPublish: share current note",
			checkCallback: (checking) => {
				const view =
					this.app.workspace.getActiveViewOfType(MarkdownView);
				if (!view || !view.file) {
					return false;
				}
				if (!checking) {
					void this.uploadActiveNote(view);
				}
				return true;
			},
		});

		this.addSettingTab(new WebPublishSettingTab(this.app, this));
	}

	private async uploadActiveNote(view: MarkdownView) {
		if (!this.ensureConfigured()) {
			return;
		}

		const file = view.file;
		if (!file) {
			new Notice("No file selected.");
			return;
		}

		const markdown =
			view.editor?.getValue() ?? (await this.app.vault.read(file));
		if (!markdown.trim()) {
			new Notice("Current note is empty.");
			return;
		}

		new Notice("Uploading note to WebPublish…");
		try {
			const title = this.resolveTitle(file);
			const preparedMarkdown = await this.expandImageEmbeds(
				markdown,
				file
			);
			const response = await this.publish(preparedMarkdown, title);
			await this.persistLinkMetadata(file, response.url);
			await this.copyLinkToClipboard(response.url);
			new Notice("Note shared successfully. Link copied to clipboard.");
		} catch (error) {
			console.error("WebPublish upload failed", error);
			const message =
				error instanceof Error ? error.message : "Upload failed";
			new Notice(`WebPublish error: ${message}`);
		}
	}

	private ensureConfigured(): boolean {
		if (!this.settings.apiBaseUrl.trim() || !this.settings.apiKey.trim()) {
			new Notice(
				"Configure WebPublish API base URL and API key in the plugin settings."
			);
			return false;
		}
		return true;
	}

	private async publish(
		markdown: string,
		title: string
	): Promise<UploadResponse> {
		const endpoint = this.buildEndpoint("/documents");
		const response = await requestUrl({
			url: endpoint,
			method: "POST",
			body: markdown,
			headers: {
				"Content-Type": "text/markdown; charset=utf-8",
				"X-API-Key": this.settings.apiKey.trim(),
				"X-Document-Title": title,
			},
		});

		if (response.status >= 400) {
			throw new Error(`Server responded with ${response.status}`);
		}
		const data = response.json as UploadResponse;
		if (!data?.url) {
			throw new Error("Unexpected response from WebPublish API");
		}
		return data;
	}

	private async expandImageEmbeds(
		markdown: string,
		sourceFile: TFile
	): Promise<string> {
		const pattern = /!\[\[([^\]]+)\]\]/g;
		let match: RegExpExecArray | null;
		let lastIndex = 0;
		let changed = false;
		let result = "";
		while ((match = pattern.exec(markdown)) !== null) {
			result += markdown.slice(lastIndex, match.index);
			const replacement = await this.convertEmbedMatch(
				match[1],
				sourceFile
			);
			if (replacement) {
				result += replacement;
				changed = true;
			} else {
				result += match[0];
			}
			lastIndex = pattern.lastIndex;
		}
		if (!changed) {
			return markdown;
		}
		result += markdown.slice(lastIndex);
		return result;
	}

	private async convertEmbedMatch(
		embedTarget: string,
		sourceFile: TFile
	): Promise<string | null> {
		const segments = embedTarget.split("|");
		const linkpath = segments.shift()?.trim();
		if (!linkpath) {
			return null;
		}
		const target = this.app.metadataCache.getFirstLinkpathDest(
			linkpath,
			sourceFile.path
		);
		if (!target || !(target instanceof TFile)) {
			return null;
		}
		const ext = (target.extension || "").toLowerCase();
		if (!IMAGE_EXTENSIONS.has(ext)) {
			return null;
		}
		const mime = this.inferImageMimeType(ext);
		if (!mime) {
			return null;
		}
		let base64: string;
		try {
			const binary = await this.app.vault.readBinary(target);
			base64 = this.arrayBufferToBase64(binary);
		} catch (error) {
			console.warn(
				`WebPublish: unable to read attachment for embed ${linkpath}`,
				error
			);
			return null;
		}
		const altText = this.pickAltText(segments, target.basename);
		const escapedAlt = this.escapeMarkdownText(altText);
		return `![${escapedAlt}](data:${mime};base64,${base64})`;
	}

	private inferImageMimeType(extension: string): string | null {
		switch (extension) {
			case "jpg":
			case "jpeg":
				return "image/jpeg";
			case "png":
				return "image/png";
			case "gif":
				return "image/gif";
			case "webp":
				return "image/webp";
			case "bmp":
				return "image/bmp";
			case "svg":
				return "image/svg+xml";
			case "tif":
			case "tiff":
				return "image/tiff";
			default:
				return null;
		}
	}

	private pickAltText(segments: string[], fallback: string): string {
		for (const segment of segments) {
			const trimmed = segment.trim();
			if (!trimmed) {
				continue;
			}
			if (/^[0-9]+(px|%)?$/i.test(trimmed)) {
				continue;
			}
			return trimmed;
		}
		return fallback;
	}

	private escapeMarkdownText(value: string): string {
		return value.replace(/[\\\[\]]/g, (match) => `\\${match}`);
	}

	private arrayBufferToBase64(buffer: ArrayBuffer): string {
		const bytes = new Uint8Array(buffer);
		let binary = "";
		const chunkSize = 0x8000;
		for (let i = 0; i < bytes.length; i += chunkSize) {
			const chunk = bytes.subarray(i, i + chunkSize);
			binary += String.fromCharCode(...chunk);
		}
		return btoa(binary);
	}

	private resolveTitle(file: TFile): string {
		const cache = this.app.metadataCache.getFileCache(file);
		const frontmatterTitle =
			cache?.frontmatter?.title ?? cache?.frontmatter?.Title ?? "";
		const normalized = String(frontmatterTitle ?? "").trim();
		if (normalized.length > 0) {
			return normalized;
		}
		return file.basename;
	}

	private async persistLinkMetadata(file: TFile, url: string) {
		const key = FRONTMATTER_KEY;
		await this.app.fileManager.processFrontMatter(file, (frontmatter) => {
			frontmatter[key] = url;
			frontmatter[`${key}-synced`] = new Date().toISOString();
		});
	}

	private async copyLinkToClipboard(link: string) {
		if (navigator?.clipboard?.writeText) {
			await navigator.clipboard.writeText(link);
			return;
		}
		try {
			const electron = (
				window as unknown as {
					require?: (module: string) => {
						clipboard: { writeText(text: string): void };
					};
				}
			).require?.("electron");
			electron?.clipboard?.writeText(link);
		} catch (error) {
			console.warn("Unable to copy link to clipboard", error);
		}
	}

	private buildEndpoint(path: string): string {
		const trimmed = this.settings.apiBaseUrl.replace(/\/$/, "");
		return `${trimmed}${path}`;
	}

	onunload() {
		// Nothing to clean up for now.
	}

	async loadSettings() {
		this.settings = Object.assign(
			{},
			DEFAULT_SETTINGS,
			await this.loadData()
		);
	}

	async saveSettings() {
		await this.saveData(this.settings);
	}
}

class WebPublishSettingTab extends PluginSettingTab {
	constructor(app: App, private readonly plugin: WebPublishPlugin) {
		super(app, plugin);
	}

	display(): void {
		const { containerEl } = this;
		containerEl.empty();
		containerEl.createEl("h2", { text: "WebPublish Settings" });

		new Setting(containerEl)
			.setName("API base URL")
			.setDesc("Example: https://publish.example.com")
			.addText((text) =>
				text
					.setPlaceholder("https://publish.example.com")
					.setValue(this.plugin.settings.apiBaseUrl)
					.onChange(async (value) => {
						this.plugin.settings.apiBaseUrl = value.trim();
						await this.plugin.saveSettings();
					})
			);

		new Setting(containerEl)
			.setName("API key")
			.setDesc(
				"Must match one of the API keys configured on the WebPublish server."
			)
			.addText((text) =>
				text
					.setPlaceholder("Paste API key")
					.setValue(this.plugin.settings.apiKey)
					.onChange(async (value) => {
						this.plugin.settings.apiKey = value.trim();
						await this.plugin.saveSettings();
					})
			);

		// Frontmatter key is a hardcoded constant and not configurable.
	}
}
