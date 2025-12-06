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

type AttachmentPayload = {
	token: string;
	filename: string;
	mimeType: string;
	data: ArrayBuffer;
};

type PreparedDocument = {
	markdown: string;
	attachments: AttachmentPayload[];
};

type MultipartPayload = {
	body: ArrayBuffer;
	contentType: string;
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
			const prepared = await this.prepareDocument(markdown, file);
			const multipart = this.buildMultipartPayload({
				markdown: prepared.markdown,
				noteFilename: file.name ?? `${file.basename}.md`,
				title,
				attachments: prepared.attachments,
			});
			const response = await this.publish(multipart, title);
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
		payload: MultipartPayload,
		title: string
	): Promise<UploadResponse> {
		const endpoint = this.buildEndpoint("/documents");
		const response = await requestUrl({
			url: endpoint,
			method: "POST",
			body: payload.body,
			headers: {
				"Content-Type": payload.contentType,
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

	private async prepareDocument(
		markdown: string,
		sourceFile: TFile
	): Promise<PreparedDocument> {
		const pattern = /!\[\[([^\]]+)\]\]/g;
		let match: RegExpExecArray | null;
		let lastIndex = 0;
		let changed = false;
		let result = "";
		const attachments: AttachmentPayload[] = [];
		while ((match = pattern.exec(markdown)) !== null) {
			result += markdown.slice(lastIndex, match.index);
			const conversion = await this.convertEmbedMatch(
				match[1],
				sourceFile
			);
			if (conversion) {
				result += conversion.replacement;
				attachments.push(conversion.attachment);
				changed = true;
			} else {
				result += match[0];
			}
			lastIndex = pattern.lastIndex;
		}
		if (!changed) {
			return { markdown, attachments: [] };
		}
		result += markdown.slice(lastIndex);
		return { markdown: result, attachments };
	}

	private async convertEmbedMatch(
		embedTarget: string,
		sourceFile: TFile
	): Promise<{
		replacement: string;
		attachment: AttachmentPayload;
	} | null> {
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
		try {
			const binary = await this.app.vault.readBinary(target);
			const token = this.generateAttachmentToken();
			const attachment: AttachmentPayload = {
				token,
				filename: this.sanitizeAttachmentName(target.name),
				mimeType: mime,
				data: binary,
			};
			const altText = this.pickAltText(segments, target.basename);
			const replacement = `<img alt="${this.escapeHtmlAttribute(
				altText
			)}" data-embed-token="${token}" />`;
			return { replacement, attachment };
		} catch (error) {
			console.warn(
				`WebPublish: unable to read attachment for embed ${linkpath}`,
				error
			);
			return null;
		}
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

	private escapeHtmlAttribute(value: string): string {
		return value.replace(/[&"'<>]/g, (match) => {
			switch (match) {
				case "&":
					return "&amp;";
				case '"':
					return "&quot;";
				case "'":
					return "&#39;";
				case "<":
					return "&lt;";
				case ">":
					return "&gt;";
				default:
					return match;
			}
		});
	}

	private buildMultipartPayload(options: {
		markdown: string;
		title: string;
		noteFilename: string;
		attachments: AttachmentPayload[];
	}): MultipartPayload {
		const boundary = `----WebPublishFormBoundary${Date.now().toString(
			16
		)}${Math.random().toString(16).slice(2)}`;
		const encoder = new TextEncoder();
		const chunks: Uint8Array[] = [];
		const pushString = (value: string) => {
			chunks.push(encoder.encode(value));
		};
		const pushBinary = (data: ArrayBuffer) => {
			chunks.push(new Uint8Array(data));
		};
		const appendField = (name: string, value: string) => {
			pushString(`--${boundary}\r\n`);
			pushString(
				`Content-Disposition: form-data; name="${name}"\r\n\r\n`
			);
			pushString(`${value}\r\n`);
		};

		appendField("title", options.title ?? "");

		const filename = this.sanitizeAttachmentName(
			options.noteFilename || "note.md"
		);
		pushString(`--${boundary}\r\n`);
		pushString(
			`Content-Disposition: form-data; name="file"; filename="${filename}"\r\n`
		);
		pushString(`Content-Type: text/markdown; charset=utf-8\r\n\r\n`);
		pushString(options.markdown);
		pushString(`\r\n`);

		for (const attachment of options.attachments) {
			pushString(`--${boundary}\r\n`);
			pushString(
				`Content-Disposition: form-data; name="attachment-${attachment.token}"; filename="${attachment.filename}"\r\n`
			);
			pushString(`Content-Type: ${attachment.mimeType}\r\n`);
			pushString(`Content-Transfer-Encoding: binary\r\n\r\n`);
			pushBinary(attachment.data);
			pushString(`\r\n`);
		}

		pushString(`--${boundary}--\r\n`);

		const totalLength = chunks.reduce(
			(sum, chunk) => sum + chunk.length,
			0
		);
		const body = new Uint8Array(totalLength);
		let offset = 0;
		for (const chunk of chunks) {
			body.set(chunk, offset);
			offset += chunk.length;
		}

		return {
			body: body.buffer,
			contentType: `multipart/form-data; boundary=${boundary}`,
		};
	}

	private sanitizeAttachmentName(name: string): string {
		const trimmed = name.trim() || "attachment";
		return trimmed.replace(/[\\/]+/g, "_").replace(/\.\.+/g, ".");
	}

	private generateAttachmentToken(length = 16): string {
		const charset =
			"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789";
		const values = new Uint8Array(length);
		if (window.crypto?.getRandomValues) {
			window.crypto.getRandomValues(values);
		} else {
			for (let i = 0; i < length; i++) {
				values[i] = Math.floor(Math.random() * 256);
			}
		}
		let token = "";
		for (const value of values) {
			token += charset[value % charset.length];
		}
		return token;
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
