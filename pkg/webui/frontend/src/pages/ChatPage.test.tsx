import { beforeEach, describe, expect, it, vi } from "vitest";
import {
	act,
	fireEvent,
	render,
	screen,
	waitFor,
} from "@testing-library/react";
import ChatPage from "./ChatPage";
import type { ChatStreamEvent } from "../types";

const mockNavigate = vi.fn();
const mockGetConversations = vi.fn();
const mockGetConversation = vi.fn();
const mockGetChatSettings = vi.fn();
const mockStreamChat = vi.fn();
const mockStreamConversation = vi.fn();
const mockGetCWDHints = vi.fn();
const mockSteerConversation = vi.fn();
const mockStopConversation = vi.fn();
const mockDeleteConversation = vi.fn();
const mockForkConversation = vi.fn();
let routeParams: { id?: string } = {};

vi.mock("react-router-dom", async () => {
	const actual =
		await vi.importActual<typeof import("react-router-dom")>(
			"react-router-dom",
		);

	return {
		...actual,
		useNavigate: () => mockNavigate,
		useParams: () => routeParams,
	};
});

vi.mock("../services/api", () => ({
	default: {
		getConversations: (...args: unknown[]) => mockGetConversations(...args),
		getConversation: (...args: unknown[]) => mockGetConversation(...args),
		getChatSettings: (...args: unknown[]) => mockGetChatSettings(...args),
		getCWDHints: (...args: unknown[]) => mockGetCWDHints(...args),
		streamChat: (...args: unknown[]) => mockStreamChat(...args),
		streamConversation: (...args: unknown[]) => mockStreamConversation(...args),
		steerConversation: (...args: unknown[]) => mockSteerConversation(...args),
		stopConversation: (...args: unknown[]) => mockStopConversation(...args),
		deleteConversation: (...args: unknown[]) => mockDeleteConversation(...args),
		forkConversation: (...args: unknown[]) => mockForkConversation(...args),
	},
}));

describe("ChatPage", () => {
	beforeEach(() => {
		vi.clearAllMocks();
		routeParams = {};
		window.localStorage.clear();
		window.HTMLElement.prototype.scrollIntoView = vi.fn();
		mockGetChatSettings.mockResolvedValue({
			currentProfile: "work",
			defaultCWD: "/workspace/default",
			profiles: [
				{ name: "default", scope: "built-in" },
				{ name: "work", scope: "repo" },
				{ name: "premium", scope: "global" },
			],
		});
		mockGetConversations.mockResolvedValue({
			conversations: [],
			hasMore: false,
			total: 0,
			limit: 10,
			offset: 0,
		});
		mockSteerConversation.mockResolvedValue({
			success: true,
			conversation_id: "conv-123",
			queued: false,
		});
		mockStreamConversation.mockRejectedValue(
			new Error("conversation is not actively streaming"),
		);
		mockStopConversation.mockResolvedValue({
			success: true,
			conversation_id: "conv-123",
			stopped: true,
		});
		mockDeleteConversation.mockResolvedValue(undefined);
		mockForkConversation.mockResolvedValue({
			success: true,
			conversation_id: "conv-copy-123",
		});
		mockGetCWDHints.mockResolvedValue({
			hints: [{ path: "/workspace/default" }],
		});
	});

	const getGreeting = (): string => {
		const hour = new Date().getHours();
		if (hour < 12) {
			return "Good morning";
		}
		if (hour < 18) {
			return "Good afternoon";
		}
		return "Good evening";
	};

	it("toggles the sidebar shell from the panel controls", async () => {
		render(<ChatPage />);

		await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());
		expect(screen.getAllByText(getGreeting())).toHaveLength(1);
		expect(screen.getByTestId("chat-sidebar-shell")).toBeInTheDocument();
		expect(screen.getByTestId("sidebar-hide-button")).toHaveClass(
			"sidebar-toggle-button",
		);

		fireEvent.click(screen.getByTestId("sidebar-hide-button"));
		expect(screen.queryByTestId("chat-sidebar-shell")).not.toBeInTheDocument();
		expect(screen.getByTestId("sidebar-collapsed-rail")).toBeInTheDocument();

		fireEvent.click(screen.getByTestId("sidebar-attached-toggle"));
		expect(screen.getByTestId("chat-sidebar-shell")).toBeInTheDocument();
	});

	it("resizes the sidebar width with the drag handle", async () => {
		render(<ChatPage />);

		await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());

		const sidebarShell = screen.getByTestId("chat-sidebar-shell");
		expect(sidebarShell.style.getPropertyValue("--sidebar-width")).toBe(
			"320px",
		);

		fireEvent.mouseDown(screen.getByTestId("chat-sidebar-resizer"), {
			clientX: 320,
		});

		await waitFor(() => expect(document.body.style.cursor).toBe("col-resize"));

		fireEvent.mouseMove(window, { clientX: 420 });
		fireEvent.mouseUp(window);

		await waitFor(() =>
			expect(
				screen
					.getByTestId("chat-sidebar-shell")
					.style.getPropertyValue("--sidebar-width"),
			).toBe("420px"),
		);
	});

	it("includes pasted image attachments in the streamed chat request", async () => {
		mockStreamChat.mockResolvedValue(undefined);

		const fileReaderResult = "data:image/png;base64,aGVsbG8=";
		const originalFileReader = window.FileReader;

		class MockFileReader {
			result: string | ArrayBuffer | null = null;
			error: DOMException | null = null;
			onload: null | (() => void) = null;
			onerror: null | (() => void) = null;

			readAsDataURL() {
				this.result = fileReaderResult;
				this.onload?.();
			}
		}

		// @ts-expect-error test shim
		window.FileReader = MockFileReader;

		render(<ChatPage />);

		await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());
		await waitFor(() => expect(mockGetChatSettings).toHaveBeenCalled());

		const textarea = screen.getByPlaceholderText("Ask kodelet anything...");
		fireEvent.change(textarea, { target: { value: "describe this image" } });

		const file = new File(["hello"], "clipboard.png", { type: "image/png" });
		fireEvent.paste(textarea, {
			clipboardData: {
				items: [
					{
						kind: "file",
						type: "image/png",
						getAsFile: () => file,
					},
				],
			},
			preventDefault: vi.fn(),
		});

		await waitFor(() =>
			expect(screen.getByAltText("clipboard.png")).toBeInTheDocument(),
		);

		fireEvent.click(screen.getByRole("button", { name: "Send" }));

		await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());
		expect(mockStreamChat).toHaveBeenCalledWith(
			expect.objectContaining({
				message: "describe this image",
				profile: "work",
				content: expect.arrayContaining([
					expect.objectContaining({
						type: "text",
						text: "describe this image",
					}),
					expect.objectContaining({
						type: "image",
						source: expect.objectContaining({
							data: "aGVsbG8=",
							media_type: "image/png",
						}),
					}),
				]),
			}),
			expect.any(Object),
		);

		window.FileReader = originalFileReader;
	});

	it("submits with Shift+Enter and keeps plain Enter for multiline editing", async () => {
		mockStreamChat.mockResolvedValue(undefined);

		render(<ChatPage />);

		await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());
		await waitFor(() => expect(mockGetChatSettings).toHaveBeenCalled());

		const textarea = screen.getByTestId("composer-textarea");
		fireEvent.change(textarea, { target: { value: "hello from shortcut" } });

		fireEvent.keyDown(textarea, {
			key: "Enter",
			shiftKey: false,
		});

		expect(mockStreamChat).not.toHaveBeenCalled();

		fireEvent.keyDown(textarea, {
			key: "Enter",
			shiftKey: true,
		});

		await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());
		expect(mockStreamChat).toHaveBeenCalledWith(
			expect.objectContaining({ message: "hello from shortcut" }),
			expect.any(Object),
		);
	});

	it("toggles the composer between expanded and restored states", async () => {
		render(<ChatPage />);

		await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());

		const toggle = screen.getByTestId("composer-expand-toggle");
		const textarea = screen.getByTestId("composer-textarea");

		expect(toggle).toHaveAttribute("aria-label", "Expand composer");
		expect(toggle).toHaveAttribute("aria-pressed", "false");
		expect(textarea).not.toHaveClass("composer-editor-expanded");

		fireEvent.click(toggle);

		expect(toggle).toHaveAttribute("aria-label", "Restore composer");
		expect(toggle).toHaveAttribute("aria-pressed", "true");
		expect(textarea).toHaveClass("composer-editor-expanded");

		fireEvent.click(toggle);

		expect(toggle).toHaveAttribute("aria-label", "Expand composer");
		expect(toggle).toHaveAttribute("aria-pressed", "false");
		expect(textarea).not.toHaveClass("composer-editor-expanded");
	});

	it("allows selecting a profile for a new conversation", async () => {
		mockStreamChat.mockResolvedValue(undefined);

		render(<ChatPage />);

		await waitFor(() => expect(mockGetChatSettings).toHaveBeenCalled());
		fireEvent.click(screen.getByTestId("sidebar-new-chat-button"));
		expect(screen.getByTestId("new-chat-dialog")).toBeInTheDocument();

		fireEvent.change(screen.getByTestId("new-chat-profile-select"), {
			target: { value: "premium" },
		});
		fireEvent.change(screen.getByLabelText("Working directory"), {
			target: { value: "/workspace/alt" },
		});

		await waitFor(() =>
			expect(mockGetCWDHints).toHaveBeenCalledWith("/workspace/alt"),
		);
		fireEvent.click(screen.getByRole("button", { name: "Use these settings" }));

		fireEvent.change(screen.getByPlaceholderText("Ask kodelet anything..."), {
			target: { value: "hello" },
		});
		fireEvent.click(screen.getByRole("button", { name: "Send" }));

		await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());
		expect(mockStreamChat).toHaveBeenCalledWith(
			expect.objectContaining({ profile: "premium", cwd: "/workspace/alt" }),
			expect.any(Object),
		);
	});

	it("shows cwd suggestions and applies a clicked suggestion", async () => {
		mockGetCWDHints.mockImplementation((query: string) => {
			if (query === "/workspace/ko") {
				return Promise.resolve({
					hints: [{ path: "/workspace/kodelet" }, { path: "/workspace/koala" }],
				});
			}
			return Promise.resolve({
				hints: [{ path: "/workspace/default" }],
			});
		});
		mockStreamChat.mockResolvedValue(undefined);

		render(<ChatPage />);

		await waitFor(() => expect(mockGetChatSettings).toHaveBeenCalled());

		fireEvent.click(screen.getByTestId("sidebar-new-chat-button"));
		const cwdInput = screen.getByLabelText("Working directory");
		fireEvent.focus(cwdInput);
		expect(screen.queryByTestId("cwd-suggestions")).not.toBeInTheDocument();
		fireEvent.change(cwdInput, { target: { value: "/workspace/ko" } });

		await waitFor(() =>
			expect(mockGetCWDHints).toHaveBeenLastCalledWith("/workspace/ko"),
		);
		await waitFor(() =>
			expect(screen.getByTestId("cwd-suggestions")).toBeInTheDocument(),
		);

		fireEvent.mouseDown(screen.getByTestId("cwd-suggestion-0"));
		fireEvent.click(screen.getByRole("button", { name: "Use these settings" }));
		expect(screen.queryByTestId("new-chat-dialog")).not.toBeInTheDocument();
		expect(
			screen.getByText(/workspace\/kodelet/),
		).toBeInTheDocument();
	});

	it("supports keyboard selection for cwd suggestions", async () => {
		mockGetCWDHints.mockImplementation((query: string) => {
			if (query === "/workspace/ko") {
				return Promise.resolve({
					hints: [{ path: "/workspace/kodelet" }, { path: "/workspace/koala" }],
				});
			}
			return Promise.resolve({
				hints: [{ path: "/workspace/default" }],
			});
		});

		render(<ChatPage />);

		await waitFor(() => expect(mockGetChatSettings).toHaveBeenCalled());

		fireEvent.click(screen.getByTestId("sidebar-new-chat-button"));
		const cwdInput = screen.getByLabelText("Working directory");
		fireEvent.focus(cwdInput);
		expect(screen.queryByTestId("cwd-suggestions")).not.toBeInTheDocument();
		fireEvent.change(cwdInput, { target: { value: "/workspace/ko" } });

		await waitFor(() =>
			expect(screen.getByTestId("cwd-suggestions")).toBeInTheDocument(),
		);

		fireEvent.keyDown(cwdInput, { key: "ArrowDown" });
		fireEvent.keyDown(cwdInput, { key: "Enter" });
		fireEvent.click(screen.getByRole("button", { name: "Use these settings" }));

		expect(
			screen.getByText(/workspace\/kodelet/),
		).toBeInTheDocument();
	});

	it("keeps the latest cwd suggestions when earlier requests resolve later", async () => {
		vi.useFakeTimers();

		const createDeferred = <T,>() => {
			let resolve!: (value: T) => void;
			const promise = new Promise<T>((resolvePromise) => {
				resolve = resolvePromise;
			});
			return { promise, resolve };
		};

		const initialRequest = createDeferred<{ hints: Array<{ path: string }> }>();
		const typedRequest = createDeferred<{ hints: Array<{ path: string }> }>();

		mockGetCWDHints.mockImplementation((query: string) => {
			if (query === "/workspace/default") {
				return initialRequest.promise;
			}
			if (query === "/workspace/ko") {
				return typedRequest.promise;
			}
			return Promise.resolve({ hints: [] });
		});

		try {
			render(<ChatPage />);

			await act(async () => {
				await Promise.resolve();
				await Promise.resolve();
			});
			expect(mockGetChatSettings).toHaveBeenCalled();
			expect(mockGetCWDHints).not.toHaveBeenCalledWith("/workspace/default");

			fireEvent.click(screen.getByTestId("sidebar-new-chat-button"));
			const cwdInput = screen.getByLabelText("Working directory");
			fireEvent.focus(cwdInput);
			fireEvent.change(cwdInput, { target: { value: "/workspace/ko" } });

			await act(async () => {
				vi.runOnlyPendingTimers();
			});

			expect(mockGetCWDHints).toHaveBeenLastCalledWith("/workspace/ko");

			await act(async () => {
				typedRequest.resolve({ hints: [{ path: "/workspace/kodelet" }] });
				await Promise.resolve();
				await Promise.resolve();
			});

			expect(screen.getByText("/workspace/kodelet")).toBeInTheDocument();

			await act(async () => {
				initialRequest.resolve({ hints: [{ path: "/workspace/default" }] });
				await Promise.resolve();
				await Promise.resolve();
			});

			expect(screen.queryByTestId("cwd-suggestion-1")).not.toBeInTheDocument();
			expect(screen.getByTestId("cwd-suggestion-0")).toHaveTextContent(
				"/workspace/kodelet",
			);
		} finally {
			vi.useRealTimers();
		}
	});

	it("submits a relative directory typed naturally", async () => {
		mockStreamChat.mockResolvedValue(undefined);

		render(<ChatPage />);

		await waitFor(() => expect(mockGetChatSettings).toHaveBeenCalled());

		fireEvent.click(screen.getByTestId("sidebar-new-chat-button"));
		fireEvent.change(screen.getByLabelText("Working directory"), {
			target: { value: "kodelet-website" },
		});
		fireEvent.click(screen.getByRole("button", { name: "Use these settings" }));
		fireEvent.change(screen.getByPlaceholderText("Ask kodelet anything..."), {
			target: { value: "hello" },
		});
		fireEvent.click(screen.getByRole("button", { name: "Send" }));

		await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());
		expect(mockStreamChat).toHaveBeenCalledWith(
			expect.objectContaining({ cwd: "kodelet-website" }),
			expect.any(Object),
		);
	});

	it("opens and closes the new chat settings dialog", async () => {
		render(<ChatPage />);

		await waitFor(() => expect(mockGetChatSettings).toHaveBeenCalled());

		fireEvent.click(screen.getByTestId("sidebar-new-chat-button"));
		expect(screen.getByTestId("new-chat-dialog")).toBeInTheDocument();

		fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
		expect(screen.queryByTestId("new-chat-dialog")).not.toBeInTheDocument();
	});

	it("shows the cwd inside the inline context for existing conversations", async () => {
		routeParams = { id: "conv-123" };
		mockGetConversation.mockResolvedValue({
			id: "conv-123",
			createdAt: "2023-01-01T00:00:00Z",
			updatedAt: "2023-01-02T00:00:00Z",
			messageCount: 1,
			cwd: "/workspace/project",
			cwdLocked: true,
			messages: [
				{
					role: "user",
					content: "hello",
				},
			],
			toolResults: {},
		});

		render(<ChatPage />);

		await waitFor(() =>
			expect(mockGetConversation).toHaveBeenCalledWith("conv-123"),
		);

		expect(screen.getByTestId("composer-inline-context")).toBeInTheDocument();
		expect(screen.getByTestId("composer-inline-context")).toHaveTextContent(
			"/workspace/project",
		);
		expect(
			screen.queryByLabelText("Working directory"),
		).not.toBeInTheDocument();
	});

	it("shows the profile inside the inline context for existing conversations", async () => {
		routeParams = { id: "conv-123" };
		mockGetConversation.mockResolvedValue({
			id: "conv-123",
			createdAt: "2023-01-01T00:00:00Z",
			updatedAt: "2023-01-02T00:00:00Z",
			messageCount: 1,
			profile: "premium",
			profileLocked: true,
			messages: [
				{
					role: "user",
					content: "hello",
				},
			],
			toolResults: {},
		});
		mockStreamChat.mockResolvedValue(undefined);

		render(<ChatPage />);

		await waitFor(() =>
			expect(mockGetConversation).toHaveBeenCalledWith("conv-123"),
		);

		expect(screen.getByTestId("composer-inline-context")).toBeInTheDocument();
		expect(screen.getByTestId("composer-inline-context")).toHaveTextContent(
			"premium",
		);
		expect(screen.queryByLabelText("Profile")).not.toBeInTheDocument();

		fireEvent.change(screen.getByPlaceholderText("Ask kodelet anything..."), {
			target: { value: "continue" },
		});
		fireEvent.click(screen.getByRole("button", { name: "Send" }));

		await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());
		expect(mockStreamChat).toHaveBeenCalledWith(
			expect.not.objectContaining({ profile: expect.anything() }),
			expect.any(Object),
		);
	});

	it("re-subscribes to an active conversation stream when reopening a conversation", async () => {
		routeParams = { id: "conv-123" };
		mockGetConversation.mockResolvedValue({
			id: "conv-123",
			createdAt: "2024-01-01T00:00:00Z",
			updatedAt: "2024-01-01T00:00:00Z",
			messageCount: 1,
			messages: [],
			toolResults: {},
		});
		mockStreamConversation.mockImplementation(async (_id, options) => {
			options.onEvent({ kind: "conversation", conversation_id: "conv-123" });
			options.onEvent({
				kind: "text-delta",
				conversation_id: "conv-123",
				delta: "hello",
			});
			options.onEvent({ kind: "done", conversation_id: "conv-123" });
		});

		render(<ChatPage />);

		await waitFor(() =>
			expect(mockGetConversation).toHaveBeenCalledWith("conv-123"),
		);
		await waitFor(() =>
			expect(mockStreamConversation).toHaveBeenCalledWith(
				"conv-123",
				expect.any(Object),
			),
		);
		await waitFor(() => expect(screen.getByText("hello")).toBeInTheDocument());
	});

	it("queues steering while a conversation is streaming", async () => {
		routeParams = { id: "conv-123" };
		mockGetConversation.mockResolvedValue({
			id: "conv-123",
			createdAt: "2023-01-01T00:00:00Z",
			updatedAt: "2023-01-02T00:00:00Z",
			messageCount: 1,
			profile: "premium",
			profileLocked: true,
			messages: [
				{
					role: "user",
					content: "hello",
				},
			],
			toolResults: {},
		});

		let streamOptions: { onEvent: (event: ChatStreamEvent) => void } | null =
			null;
		mockStreamChat.mockImplementation(async (_request, options) => {
			streamOptions = options as { onEvent: (event: ChatStreamEvent) => void };
			return new Promise(() => undefined);
		});

		render(<ChatPage />);

		await waitFor(() =>
			expect(mockGetConversation).toHaveBeenCalledWith("conv-123"),
		);

		fireEvent.change(screen.getByPlaceholderText("Ask kodelet anything..."), {
			target: { value: "continue" },
		});
		fireEvent.click(screen.getByRole("button", { name: "Send" }));

		await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());
		expect(screen.getByRole("button", { name: "Stop" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Steer" })).toBeInTheDocument();

		await act(async () => {
			streamOptions?.onEvent({
				kind: "tool-use",
				tool_call_id: "tool-1",
				tool_name: "search",
				input: "{}",
			});
		});

		fireEvent.change(
			screen.getByPlaceholderText("Steer the active conversation…"),
			{
				target: { value: "Focus on tests" },
			},
		);

		await waitFor(() =>
			expect(screen.getByRole("button", { name: "Steer" })).toBeEnabled(),
		);

		fireEvent.click(screen.getByRole("button", { name: "Steer" }));

		await waitFor(() =>
			expect(mockSteerConversation).toHaveBeenCalledWith(
				"conv-123",
				"Focus on tests",
			),
		);

		await act(async () => {
			streamOptions?.onEvent({
				kind: "conversation",
				conversation_id: "conv-123",
			});
		});
	});

	it("allows sidebar navigation while a conversation is streaming", async () => {
		mockGetConversations.mockResolvedValue({
			conversations: [
				{
					id: "conv-123",
					createdAt: "2024-01-01T00:00:00Z",
					updatedAt: "2024-01-01T00:00:00Z",
					messageCount: 1,
					summary: "Active conversation",
				},
				{
					id: "conv-456",
					createdAt: "2024-01-01T00:00:00Z",
					updatedAt: "2024-01-01T00:00:00Z",
					messageCount: 1,
					summary: "Other conversation",
				},
			],
			hasMore: false,
			total: 2,
			limit: 40,
			offset: 0,
		});

		mockStreamChat.mockImplementation(async (_request, options) => {
			options.onEvent({ kind: "conversation", conversation_id: "conv-123" });
			return new Promise(() => undefined);
		});

		render(<ChatPage />);

		await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());

		fireEvent.change(screen.getByPlaceholderText("Ask kodelet anything..."), {
			target: { value: "hello" },
		});
		fireEvent.click(screen.getByRole("button", { name: "Send" }));

		await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());

		fireEvent.click(screen.getByTestId("sidebar-hide-button"));
		expect(screen.queryByTestId("chat-sidebar-shell")).not.toBeInTheDocument();

		fireEvent.click(screen.getByTestId("sidebar-attached-toggle"));
		fireEvent.click(screen.getByRole("button", { name: /No directory 1/i }));
		fireEvent.click(
			screen.getAllByRole("button", { name: /Other conversation/i })[0],
		);

		expect(mockNavigate).toHaveBeenCalledWith("/c/conv-456");
	});

	it("ignores stale new-chat stream events after switching conversations", async () => {
		mockGetConversation.mockImplementation(async (id: string) => ({
			id,
			createdAt: "2024-01-01T00:00:00Z",
			updatedAt: "2024-01-01T00:00:00Z",
			messageCount: 1,
			messages:
				id === "conv-456"
					? [
							{
								role: "user",
								content: "Existing conversation",
							},
						]
					: [],
			toolResults: {},
		}));

		let streamOptions: { onEvent: (event: ChatStreamEvent) => void } | null =
			null;
		let resolveStream: (() => void) | null = null;
		mockStreamChat.mockImplementation(
			async (_request, options) =>
				new Promise<void>((resolve) => {
					streamOptions = options as {
						onEvent: (event: ChatStreamEvent) => void;
					};
					resolveStream = resolve;
				}),
		);

		const { rerender } = render(<ChatPage />);

		await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());

		fireEvent.change(screen.getByPlaceholderText("Ask kodelet anything..."), {
			target: { value: "hello" },
		});
		fireEvent.click(screen.getByRole("button", { name: "Send" }));

		await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());

		await act(async () => {
			streamOptions?.onEvent({
				kind: "conversation",
				conversation_id: "conv-123",
			});
		});

		routeParams = { id: "conv-456" };
		rerender(<ChatPage />);

		await waitFor(() =>
			expect(mockGetConversation).toHaveBeenCalledWith("conv-456"),
		);
		await waitFor(() =>
			expect(screen.getByText("Existing conversation")).toBeInTheDocument(),
		);

		await act(async () => {
			streamOptions?.onEvent({
				kind: "text-delta",
				conversation_id: "conv-123",
				delta: "Leaked streamed text",
			});
			resolveStream?.();
		});

		expect(screen.queryByText("Leaked streamed text")).not.toBeInTheDocument();
		expect(mockNavigate).not.toHaveBeenCalledWith("/c/conv-123");
		expect(mockGetConversation).not.toHaveBeenCalledWith("conv-123");
	});

	it("adds a newly started conversation to the sidebar before refresh", async () => {
		mockGetConversations.mockResolvedValue({
			conversations: [
				{
					id: "conv-456",
					createdAt: "2024-01-01T00:00:00Z",
					updatedAt: "2024-01-01T00:00:00Z",
					messageCount: 1,
					summary: "Existing conversation",
				},
			],
			hasMore: false,
			total: 1,
			limit: 40,
			offset: 0,
		});

		mockGetConversation.mockImplementation(async (id: string) => ({
			id,
			createdAt: "2024-01-01T00:00:00Z",
			updatedAt: "2024-01-01T00:00:00Z",
			messageCount: 1,
			messages: [
				{
					role: "user",
					content:
						id === "conv-456" ? "Existing conversation" : "Brand new task",
				},
			],
			toolResults: {},
		}));

		let streamOptions: { onEvent: (event: ChatStreamEvent) => void } | null =
			null;
		mockStreamChat.mockImplementation(
			async (_request, options) =>
				new Promise<void>(() => {
					streamOptions = options as {
						onEvent: (event: ChatStreamEvent) => void;
					};
				}),
		);

		const { rerender } = render(<ChatPage />);

		await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());

		fireEvent.change(screen.getByPlaceholderText("Ask kodelet anything..."), {
			target: { value: "Brand new task" },
		});
		fireEvent.click(screen.getByRole("button", { name: "Send" }));

		await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());

		await act(async () => {
			streamOptions?.onEvent({
				kind: "conversation",
				conversation_id: "conv-123",
			});
		});

		expect(
			screen.getAllByRole("button", { name: /Brand new task/i })[0],
		).toBeInTheDocument();

		routeParams = { id: "conv-456" };
		rerender(<ChatPage />);

		await waitFor(() =>
			expect(mockGetConversation).toHaveBeenCalledWith("conv-456"),
		);
		expect(
			screen.getAllByRole("button", { name: /Brand new task/i })[0],
		).toBeInTheDocument();
	});

	it("forks a conversation from the sidebar menu", async () => {
		mockGetConversations.mockResolvedValue({
			conversations: [
				{
					id: "conv-123",
					createdAt: "2024-01-01T00:00:00Z",
					updatedAt: "2024-01-01T00:00:00Z",
					messageCount: 1,
					summary: "Enabled resumable webUI conversation",
				},
			],
			hasMore: false,
			total: 1,
			limit: 40,
			offset: 0,
		});

		render(<ChatPage />);

		await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());

		fireEvent.click(
			screen.getByRole("button", {
				name: /More actions for Enabled resumable webUI conversation/i,
			}),
		);
		fireEvent.click(screen.getByRole("menuitem", { name: "Copy" }));

		await waitFor(() =>
			expect(mockForkConversation).toHaveBeenCalledWith("conv-123"),
		);
		await waitFor(() =>
			expect(mockNavigate).toHaveBeenCalledWith("/c/conv-copy-123"),
		);
	});

	it("deletes the active conversation from the sidebar menu", async () => {
		routeParams = { id: "conv-123" };
		mockGetConversations.mockResolvedValue({
			conversations: [
				{
					id: "conv-123",
					createdAt: "2024-01-01T00:00:00Z",
					updatedAt: "2024-01-01T00:00:00Z",
					messageCount: 1,
					summary: "Enabled resumable webUI conversation",
				},
			],
			hasMore: false,
			total: 1,
			limit: 40,
			offset: 0,
		});
		mockGetConversation.mockResolvedValue({
			id: "conv-123",
			createdAt: "2024-01-01T00:00:00Z",
			updatedAt: "2024-01-01T00:00:00Z",
			messageCount: 1,
			summary: "Enabled resumable webUI conversation",
			messages: [],
			toolResults: {},
		});

		render(<ChatPage />);

		await waitFor(() =>
			expect(mockGetConversation).toHaveBeenCalledWith("conv-123"),
		);

		fireEvent.click(
			screen.getByRole("button", {
				name: /More actions for Enabled resumable webUI conversation/i,
			}),
		);
		fireEvent.click(screen.getByRole("menuitem", { name: "Delete" }));

		await waitFor(() =>
			expect(mockDeleteConversation).toHaveBeenCalledWith("conv-123"),
		);
		await waitFor(() => expect(mockNavigate).toHaveBeenCalledWith("/"));
	});

	it("does not queue steering before the stream shows another backend turn is possible", async () => {
		routeParams = { id: "conv-123" };
		mockGetConversation.mockResolvedValue({
			id: "conv-123",
			createdAt: "2023-01-01T00:00:00Z",
			updatedAt: "2023-01-02T00:00:00Z",
			messageCount: 1,
			profile: "premium",
			profileLocked: true,
			messages: [
				{
					role: "user",
					content: "hello",
				},
			],
			toolResults: {},
		});

		mockStreamChat.mockImplementation(async () => new Promise(() => undefined));

		render(<ChatPage />);

		await waitFor(() =>
			expect(mockGetConversation).toHaveBeenCalledWith("conv-123"),
		);

		fireEvent.change(screen.getByPlaceholderText("Ask kodelet anything..."), {
			target: { value: "continue" },
		});
		fireEvent.click(screen.getByRole("button", { name: "Send" }));

		await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());

		const steerButton = screen.getByRole("button", { name: "Steer" });
		expect(steerButton).toBeDisabled();

		const textarea = screen.getByPlaceholderText(
			"Steering becomes available if the agent starts another turn…",
		);
		fireEvent.change(textarea, { target: { value: "Focus on tests" } });
		fireEvent.keyDown(textarea, {
			key: "Enter",
			shiftKey: false,
			preventDefault: vi.fn(),
		});

		expect(mockSteerConversation).not.toHaveBeenCalled();
	});

	it("keeps stop unavailable until a new conversation receives a server id", async () => {
		mockStreamChat.mockImplementation(async () => new Promise(() => undefined));

		render(<ChatPage />);

		await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());

		fireEvent.change(screen.getByPlaceholderText("Ask kodelet anything..."), {
			target: { value: "hello" },
		});
		fireEvent.click(screen.getByRole("button", { name: "Send" }));

		await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());

		expect(mockStreamChat.mock.calls[0]?.[0]?.conversationId).toBeUndefined();
		expect(screen.getByRole("button", { name: "Starting…" })).toBeDisabled();
		expect(screen.getByTestId("sidebar-new-chat-button")).toBeDisabled();
		expect(mockStopConversation).not.toHaveBeenCalled();
	});

	it("allows starting a new chat once streaming has a conversation id", async () => {
		mockStreamChat.mockImplementation(async (_request, options) => {
			options.onEvent({ kind: "conversation", conversation_id: "conv-123" });
			return new Promise(() => undefined);
		});

		render(<ChatPage />);

		await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());

		fireEvent.change(screen.getByPlaceholderText("Ask kodelet anything..."), {
			target: { value: "hello" },
		});
		fireEvent.click(screen.getByRole("button", { name: "Send" }));

		await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());
		await waitFor(() =>
			expect(screen.getByTestId("sidebar-new-chat-button")).toBeEnabled(),
		);

		fireEvent.click(screen.getByTestId("sidebar-new-chat-button"));

		expect(mockNavigate).toHaveBeenCalledWith("/");
		expect(screen.getByTestId("new-chat-dialog")).toBeInTheDocument();
	});

	it("groups recent chats by cwd and lets directories collapse independently", async () => {
		mockGetConversations
			.mockResolvedValueOnce({
				conversations: Array.from({ length: 12 }, (_, index) => ({
					id: `conv-${index + 1}`,
					createdAt: `2024-01-${String(index + 1).padStart(2, "0")}T00:00:00Z`,
					updatedAt: `2024-01-${String(index + 1).padStart(2, "0")}T00:00:00Z`,
					messageCount: 1,
					summary: `Conversation ${index + 1}`,
					cwd:
						index < 6
							? "/workspace/a"
							: index < 10
								? "/workspace/b"
								: "/workspace/c",
				})),
				hasMore: false,
				total: 12,
				limit: 100,
				offset: 0,
			});

		render(<ChatPage />);

		await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());
		expect(screen.getByText("/workspace/a")).toBeInTheDocument();
		expect(screen.getByText("/workspace/b")).toBeInTheDocument();
		expect(screen.getByText("/workspace/c")).toBeInTheDocument();

		fireEvent.click(screen.getByRole("button", { name: /\/workspace\/a 6/i }));
		await waitFor(() => expect(screen.getByText("Conversation 1")).toBeInTheDocument());

		fireEvent.click(screen.getByRole("button", { name: /\/workspace\/b 4/i }));
		await waitFor(() => expect(screen.getByText("Conversation 7")).toBeInTheDocument());

		fireEvent.click(screen.getByRole("button", { name: /\/workspace\/b/i }));

		await waitFor(() =>
			expect(screen.queryByText("Conversation 7")).not.toBeInTheDocument(),
		);
		expect(screen.getByText("Conversation 1")).toBeInTheDocument();

		fireEvent.click(screen.getByRole("button", { name: /\/workspace\/b/i }));
		await waitFor(() => expect(screen.getByText("Conversation 7")).toBeInTheDocument());
	});

	it("shows the full cwd label in recent chats and hides sidebar metadata", async () => {
		mockGetConversations.mockResolvedValue({
			conversations: [
				{
					id: "conv-123",
					createdAt: "2024-01-01T00:00:00Z",
					updatedAt: "2024-01-01T00:00:00Z",
					messageCount: 1,
					summary: "Conversation 1",
					cwd: "/home/jingkaihe/workspace/kodelet",
				},
			],
			hasMore: false,
			total: 1,
			limit: 100,
			offset: 0,
		});

		routeParams = { id: "conv-123" };
		render(<ChatPage />);

		await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());
		expect(
			screen.getByText("/home/jingkaihe/workspace/kodelet"),
		).toBeInTheDocument();
		expect(screen.queryByText(/^ID:/)).not.toBeInTheDocument();
		expect(screen.queryByText(/^Mode:/)).not.toBeInTheDocument();
	});

	it("reveals more conversations within an expanded directory", async () => {
		mockGetConversations.mockResolvedValue({
			conversations: Array.from({ length: 12 }, (_, index) => ({
				id: `conv-${index + 1}`,
				createdAt: `2024-01-${String(index + 1).padStart(2, "0")}T00:00:00Z`,
				updatedAt: `2024-01-${String(index + 1).padStart(2, "0")}T00:00:00Z`,
				messageCount: 1,
				summary: `Conversation ${index + 1}`,
				cwd: "/workspace/kodelet",
			})),
			hasMore: false,
			total: 12,
			limit: 100,
			offset: 0,
		});

		render(<ChatPage />);

		await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());
		expect(screen.getByText("Conversation 10")).toBeInTheDocument();
		expect(screen.queryByText("Conversation 11")).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Show 2 more" })).toBeInTheDocument();

		fireEvent.click(screen.getByRole("button", { name: "Show 2 more" }));

		await waitFor(() => expect(screen.getByText("Conversation 11")).toBeInTheDocument());
		expect(screen.getByText("Conversation 12")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Show less" })).toBeInTheDocument();
	});

	it("lets an expanded directory show less before all conversations are revealed", async () => {
		mockGetConversations.mockResolvedValue({
			conversations: Array.from({ length: 25 }, (_, index) => ({
				id: `conv-${index + 1}`,
				createdAt: `2024-01-${String((index % 28) + 1).padStart(2, "0")}T00:00:00Z`,
				updatedAt: `2024-01-${String((index % 28) + 1).padStart(2, "0")}T00:00:00Z`,
				messageCount: 1,
				summary: `Conversation ${index + 1}`,
				cwd: "/workspace/kodelet",
			})),
			hasMore: false,
			total: 25,
			limit: 100,
			offset: 0,
		});

		render(<ChatPage />);

		await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());
		expect(screen.getByRole("button", { name: "Show 10 more" })).toBeInTheDocument();

		fireEvent.click(screen.getByRole("button", { name: "Show 10 more" }));

		await waitFor(() => expect(screen.getByText("Conversation 20")).toBeInTheDocument());
		expect(screen.queryByText("Conversation 21")).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Show less" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Show 5 more" })).toBeInTheDocument();

		fireEvent.click(screen.getByRole("button", { name: "Show less" }));

		await waitFor(() =>
			expect(screen.queryByText("Conversation 11")).not.toBeInTheDocument(),
		);
		expect(screen.getByRole("button", { name: "Show 10 more" })).toBeInTheDocument();
	});

	it("shows compact new chat context text in the composer", async () => {
		render(<ChatPage />);

		await waitFor(() => expect(mockGetChatSettings).toHaveBeenCalled());
		expect(
			screen.getByText(/work · \/workspace\/default/),
		).toBeInTheDocument();
		expect(screen.getByText("Shift+Enter to send")).toBeInTheDocument();
	});

	it("shows compact usage metadata below the transcript when available", async () => {
		routeParams = { id: "conv-123" };
		const updatedAt = new Date(Date.now() - 3 * 60 * 1000).toISOString();
		mockGetConversation.mockResolvedValue({
			id: "conv-123",
			createdAt: "2023-01-02T11:00:00Z",
			updatedAt,
			messageCount: 1,
			messages: [
				{
					role: "user",
					content: "hello",
				},
			],
			toolResults: {},
			usage: {
				currentContextWindow: 14200,
				maxContextWindow: 272000,
				inputTokens: 1200,
				outputTokens: 340,
				cacheReadInputTokens: 8000,
				cacheCreationInputTokens: 2200,
				inputCost: 0,
				outputCost: 0,
				cacheCreationCost: 0,
				cacheReadCost: 0,
			},
		});

		render(<ChatPage />);

		await waitFor(() =>
			expect(mockGetConversation).toHaveBeenCalledWith("conv-123"),
		);

		const meta = screen.getByTestId("transcript-meta-strip");
		expect(meta).toHaveTextContent("14.2K/272K (5%) context");
		expect(meta).toHaveTextContent("in 1.2K");
		expect(meta).toHaveTextContent("out 340");
		expect(meta).toHaveTextContent("cr 8K");
		expect(meta).toHaveTextContent("cw 2.2K");
		expect(meta).toHaveTextContent("$0.0000");
		expect(meta.textContent).toContain(", in 1.2K, out 340, cr 8K, cw 2.2K, $0.0000,");
		expect(meta.textContent).toMatch(/\d+m ago|just now/);
	});

	it("updates compact usage metadata when a streamed usage event arrives", async () => {
		routeParams = { id: "conv-123" };
		mockGetConversation.mockResolvedValue({
			id: "conv-123",
			createdAt: "2023-01-02T11:00:00Z",
			updatedAt: "2023-01-02T11:05:00Z",
			messageCount: 1,
			messages: [
				{
					role: "user",
					content: "hello",
				},
			],
			toolResults: {},
			usage: {
				currentContextWindow: 1000,
				maxContextWindow: 272000,
				inputTokens: 100,
				outputTokens: 20,
				inputCost: 0,
				outputCost: 0,
				cacheCreationCost: 0,
				cacheReadCost: 0,
			},
		});

		const streamListeners: Array<(event: ChatStreamEvent) => void> = [];
		mockStreamConversation.mockImplementation(async (_id, options) => {
			streamListeners.push(
				(options as { onEvent: (event: ChatStreamEvent) => void }).onEvent,
			);
			return new Promise(() => undefined);
		});

		render(<ChatPage />);

		await waitFor(() =>
			expect(mockGetConversation).toHaveBeenCalledWith("conv-123"),
		);
		await waitFor(() =>
			expect(mockStreamConversation).toHaveBeenCalledWith(
				"conv-123",
				expect.any(Object),
			),
		);
		expect(streamListeners).toHaveLength(1);

		expect(screen.getByTestId("transcript-meta-strip")).toHaveTextContent(
			"in 100",
		);

		await act(async () => {
			streamListeners[0]?.({
				kind: "usage",
				conversation_id: "conv-123",
				usage: {
					currentContextWindow: 2400,
					maxContextWindow: 272000,
					inputTokens: 100,
					outputTokens: 140,
					cacheReadInputTokens: 50,
					inputCost: 0.0001,
					outputCost: 0.0002,
					cacheCreationCost: 0,
					cacheReadCost: 0,
				},
			});
		});

		await waitFor(() => {
			const meta = screen.getByTestId("transcript-meta-strip");
			expect(meta).toHaveTextContent("2.4K/272K (1%) context");
			expect(meta).toHaveTextContent("in 100");
			expect(meta).toHaveTextContent("out 140");
			expect(meta).toHaveTextContent("cr 50");
			expect(meta).toHaveTextContent("$0.0003");
		});

		expect(mockStreamConversation).toHaveBeenCalledTimes(1);

		await act(async () => {
			streamListeners[0]?.({
				kind: "text-delta",
				conversation_id: "conv-123",
				delta: "stream continues",
			});
		});

		expect(screen.getByText("stream continues")).toBeInTheDocument();
	});

	it("disables delete for the active conversation while it is streaming", async () => {
		routeParams = { id: "conv-123" };

		mockGetConversations.mockResolvedValue({
			conversations: [
				{
					id: "conv-123",
					createdAt: "2024-01-01T00:00:00Z",
					updatedAt: "2024-01-01T00:00:00Z",
					messageCount: 1,
					summary: "Enabled resumable webUI conversation",
				},
			],
			hasMore: false,
			total: 1,
			limit: 40,
			offset: 0,
		});
		mockGetConversation.mockResolvedValue({
			id: "conv-123",
			createdAt: "2024-01-01T00:00:00Z",
			updatedAt: "2024-01-01T00:00:00Z",
			messageCount: 1,
			summary: "Enabled resumable webUI conversation",
			messages: [],
			toolResults: {},
		});
		mockStreamChat.mockImplementation(
			async (_request, options) =>
				new Promise(() => {
					options.onEvent({
						kind: "conversation",
						conversation_id: "conv-123",
					} as ChatStreamEvent);
				}),
		);

		render(<ChatPage />);

		await waitFor(() =>
			expect(mockGetConversation).toHaveBeenCalledWith("conv-123"),
		);

		fireEvent.change(screen.getByPlaceholderText("Ask kodelet anything..."), {
			target: { value: "continue" },
		});
		fireEvent.click(screen.getByRole("button", { name: "Send" }));

		await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());

		fireEvent.click(
			screen.getByRole("button", {
				name: /More actions for Enabled resumable webUI conversation/i,
			}),
		);
		expect(screen.getByRole("menuitem", { name: "Delete" })).toBeDisabled();
		expect(mockDeleteConversation).not.toHaveBeenCalled();
	});

	it("stops an active streaming conversation", async () => {
		const abortSpy = vi.fn();
		const originalAbortController = global.AbortController;
		let rejectStream: ((reason?: unknown) => void) | null = null;

		class MockAbortController {
			signal = {} as AbortSignal;
			abort = abortSpy;
		}

		global.AbortController =
			MockAbortController as unknown as typeof AbortController;

		mockStreamChat.mockImplementation(
			async (_request, options) =>
				new Promise((_, reject) => {
					options.onEvent({
						kind: "conversation",
						conversation_id: "conv-123",
					} as ChatStreamEvent);
					rejectStream = reject;
				}),
		);

		render(<ChatPage />);

		await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());

		fireEvent.change(screen.getByPlaceholderText("Ask kodelet anything..."), {
			target: { value: "hello" },
		});
		fireEvent.click(screen.getByRole("button", { name: "Send" }));

		await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());
		fireEvent.click(screen.getByRole("button", { name: "Stop" }));

		expect(abortSpy).toHaveBeenCalled();
		await waitFor(() =>
			expect(mockStopConversation).toHaveBeenCalledWith("conv-123"),
		);
		expect(screen.getByRole("button", { name: "Stop" })).toBeInTheDocument();
		expect(
			screen.queryByRole("button", { name: "Send" }),
		).not.toBeInTheDocument();

		await act(async () => {
			rejectStream?.(
				new DOMException("The operation was aborted", "AbortError"),
			);
		});

		await waitFor(() =>
			expect(screen.getByRole("button", { name: "Send" })).toBeInTheDocument(),
		);

		global.AbortController = originalAbortController;
	});
});
