import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { AuthPrincipal, Conversation } from "../../types";
import ChatSidebar, { ConversationSearchDialog, groupConversationsByCwd } from "./ChatSidebar";

const renderSidebar = (authPrincipal?: AuthPrincipal | null) =>
	render(
		<div className="h-screen w-[320px]">
			<ChatSidebar
				activeConversationId={null}
				authPrincipal={authPrincipal}
				conversations={[]}
				loading={false}
				onDeleteConversation={vi.fn()}
				onForkConversation={vi.fn()}
				onNewChat={vi.fn()}
				onSearch={vi.fn()}
				onSelectConversation={vi.fn()}
			/>
		</div>,
	);

describe("ChatSidebar hierarchy", () => {
	const conversation = (id: string, parent?: string, day = 1): Conversation => ({
		id,
		summary: id,
		cwd: "/workspace/main",
		createdAt: "2026-09-01T00:00:00Z",
		updatedAt: `2026-09-${String(day).padStart(2, "0")}T00:00:00Z`,
		messageCount: 1,
		metadata: parent ? { parent_conversation_id: parent } : undefined,
	});
	const callbacks = () => ({
		loading: false,
		onDeleteConversation: vi.fn(),
		onForkConversation: vi.fn(),
		onNewChat: vi.fn(),
		onSearch: vi.fn(),
		onSelectConversation: vi.fn(),
	});

	it("orders trees by descendant activity and groups children under the root workspace", () => {
		const input = [
			{ ...conversation("grandchild", "child", 9), cwd: "/workspace/other" },
			conversation("other-root", undefined, 8),
			conversation("sibling", "parent", 7),
			conversation("child", "parent", 2),
			conversation("parent"),
		];
		const groups = groupConversationsByCwd(input);
		expect(groups).toHaveLength(1);
		expect(groups[0].cwd).toBe("/workspace/main");
		expect(groups[0].conversations.map(({ id, depth }) => [id, depth])).toEqual([
			["parent", 0], ["child", 1], ["grandchild", 2], ["sibling", 1], ["other-root", 0],
		]);
		expect(input[0].id).toBe("grandchild");
		expect(input[0]).not.toHaveProperty("depth");
	});

	it("keeps missing parents and malformed cycles visible exactly once without using fork lineage", () => {
		const groups = groupConversationsByCwd([
			conversation("orphan", "missing"),
			conversation("self", "self"),
			conversation("a", "b"),
			conversation("b", "a"),
			conversation("nested", "b"),
			{ ...conversation("copy"), metadata: { conversation_fork: { source_conversation_id: "a" } } },
			conversation("orphan", "missing"),
		]);
		const rows = groups.flatMap((group) => group.conversations);
		expect(rows).toHaveLength(6);
		expect(new Set(rows.map(({ id }) => id)).size).toBe(6);
		for (const id of ["orphan", "self", "copy"]) {
			expect(rows.find((row) => row.id === id)?.depth).toBe(0);
		}
		expect(rows.find((row) => row.id === "nested")?.depth).toBeGreaterThan(0);
	});

	it("supports projected parent IDs and deep chains without recursive rendering", () => {
		const input = Array.from({ length: 2000 }, (_, index) => ({
			...conversation(`node-${index}`),
			parentConversationId: index ? `node-${index - 1}` : undefined,
		}));
		const rows = groupConversationsByCwd(input.reverse())[0].conversations;
		expect(rows).toHaveLength(2000);
		expect(rows[0].id).toBe("node-0");
		expect(rows[1999].depth).toBe(1999);
	});

	it("preserves child selection, running indicators and per-conversation actions", () => {
		const props = callbacks();
		render(<ChatSidebar {...props} activeConversationId="child" conversations={[
			{ ...conversation("child", "parent", 3), isRunning: true },
			{ ...conversation("parent"), cwd: undefined },
			{ ...conversation("orphan", "missing", 2), cwd: undefined },
		]} />);
		const rows = screen.getAllByTestId(/^conversation-row-/);
		expect(rows.map((row) => row.dataset.testid)).toEqual([
			"conversation-row-parent", "conversation-row-child", "conversation-row-orphan",
		]);
		const child = screen.getByTestId("conversation-row-child");
		expect(child).toHaveAttribute("data-depth", "1");
		expect(child).toHaveClass("active", "is-child");
		expect(screen.getByTestId("conversation-row-parent")).not.toHaveClass("is-child");
		expect(child.querySelector(".conversation-branch")).toHaveClass("last-child");
		const indicator = within(child).getByTestId("conversation-running-indicator-child");
		expect(indicator.querySelector(".spinner-glyph")).toHaveTextContent("⣾");
		fireEvent.click(within(child).getByRole("button", { name: "child Running" }));
		expect(props.onSelectConversation).toHaveBeenCalledWith("child");
		fireEvent.click(within(child).getByRole("button", { name: "More actions for child" }));
		expect(screen.getByRole("menuitem", { name: "Delete" })).toBeDisabled();
		fireEvent.click(screen.getByRole("menuitem", { name: "Copy" }));
		expect(props.onForkConversation).toHaveBeenCalledWith("child");
		expect(within(screen.getByTestId("conversation-row-orphan")).getByText("Child")).toBeInTheDocument();
	});

	it("keeps whole trees together at Show more boundaries and reveals active descendants", () => {
		const props = callbacks();
		const conversations = [
			...Array.from({ length: 9 }, (_, index) => conversation(`recent-${index}`, undefined, 9)),
			conversation("parent", undefined, 8),
			...Array.from({ length: 3 }, (_, index) => conversation(`child-${index}`, "parent", 7)),
			...Array.from({ length: 10 }, (_, index) => conversation(`older-${index}`, undefined, 6)),
			conversation("last-parent", undefined, 2),
			conversation("last-child", "last-parent"),
		];
		const { rerender } = render(<ChatSidebar {...props} activeConversationId={null} conversations={conversations} />);
		expect(screen.getAllByTestId(/^conversation-row-/)).toHaveLength(13);
		expect(screen.getByTestId("conversation-row-child-2")).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Show 10 more" }));
		expect(screen.getAllByTestId(/^conversation-row-/)).toHaveLength(23);
		fireEvent.click(screen.getByRole("button", { name: "Show less" }));
		expect(screen.getAllByTestId(/^conversation-row-/)).toHaveLength(13);
		rerender(<ChatSidebar {...props} activeConversationId="last-child" conversations={conversations} />);
		expect(screen.getByTestId("conversation-row-last-child")).toHaveClass("active");
		expect(screen.getByTestId("conversation-row-last-parent")).toBeInTheDocument();
	});
});

describe("ChatSidebar account menu", () => {
	it("only renders the account control for an OIDC principal", () => {
		const { rerender } = renderSidebar();

		expect(screen.queryByRole("button", { name: /account menu/i })).not.toBeInTheDocument();

		rerender(
			<div className="h-screen w-[320px]">
				<ChatSidebar
					activeConversationId={null}
					authPrincipal={{ id: "token", roles: ["admin"] }}
					conversations={[]}
					loading={false}
					onDeleteConversation={vi.fn()}
					onForkConversation={vi.fn()}
					onNewChat={vi.fn()}
					onSearch={vi.fn()}
					onSelectConversation={vi.fn()}
				/>
			</div>,
		);

		expect(screen.queryByRole("button", { name: /account menu/i })).not.toBeInTheDocument();
	});

	it("shows the abbreviated user name and a Lucide sign-out action", async () => {
		const user = userEvent.setup();
		renderSidebar({
			id: "https://issuer.example.com|jingkai-he",
			issuer: "https://issuer.example.com",
			subject: "jingkai-he",
			name: "Jingkai He",
			email: "jingkai@example.com",
			roles: ["user"],
		});

		const accountButton = screen.getByRole("button", {
			name: "Jingkai He account menu",
		});
		expect(within(accountButton).getByText("JH")).toBeInTheDocument();
		expect(within(accountButton).getByText("J He")).toBeInTheDocument();

		await user.click(accountButton);

		expect(accountButton).toHaveAttribute("aria-expanded", "true");
		const signOut = screen.getByRole("menuitem", { name: "Sign out" });
		expect(signOut).toHaveAttribute("href", "/auth/logout");
		expect(signOut.querySelector("svg")).toHaveClass("lucide-log-out");

		fireEvent.keyDown(document, { key: "Escape" });
		expect(screen.queryByRole("menuitem", { name: "Sign out" })).not.toBeInTheDocument();
		expect(accountButton).toHaveAttribute("aria-expanded", "false");
	});
});

describe("ChatSidebar provider settings", () => {
	it("shows provider settings inside the administrator account menu", async () => {
		const user = userEvent.setup();
		const onOpenProviderSettings = vi.fn();
		render(
			<div className="h-screen w-[320px]">
				<ChatSidebar
					activeConversationId={null}
					authPrincipal={{
						id: "https://issuer.example.com|admin",
						issuer: "https://issuer.example.com",
						subject: "admin",
						name: "Admin User",
						roles: ["admin"],
					}}
					conversations={[]}
					loading={false}
					onDeleteConversation={vi.fn()}
					onForkConversation={vi.fn()}
					onNewChat={vi.fn()}
					onOpenProviderSettings={onOpenProviderSettings}
					onSearch={vi.fn()}
					onSelectConversation={vi.fn()}
				/>
			</div>,
		);

		expect(screen.queryByRole("menuitem", { name: "Provider settings" })).not.toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Admin User account menu" }));
		const providerSettings = screen.getByRole("menuitem", { name: "Provider settings" });
		expect(providerSettings.querySelector("svg")).toHaveClass("lucide-settings-2");
		await user.click(providerSettings);
		expect(onOpenProviderSettings).toHaveBeenCalledOnce();
		expect(screen.queryByRole("menuitem", { name: "Provider settings" })).not.toBeInTheDocument();
	});

	it("does not expose server provider credentials to non-admin users", async () => {
		const user = userEvent.setup();
		render(
			<div className="h-screen w-[320px]">
				<ChatSidebar
					activeConversationId={null}
					authPrincipal={{
						id: "https://issuer.example.com|user",
						issuer: "https://issuer.example.com",
						subject: "user",
						name: "Regular User",
						roles: ["user"],
					}}
					conversations={[]}
					loading={false}
					onDeleteConversation={vi.fn()}
					onForkConversation={vi.fn()}
					onNewChat={vi.fn()}
					onOpenProviderSettings={vi.fn()}
					onSearch={vi.fn()}
					onSelectConversation={vi.fn()}
				/>
			</div>,
		);

		await user.click(screen.getByRole("button", { name: "Regular User account menu" }));
		expect(screen.queryByRole("menuitem", { name: "Provider settings" })).not.toBeInTheDocument();
	});
});

describe("ChatSidebar conversation actions", () => {
	it("delegates search to a dialog trigger in the header", () => {
		const onSearch = vi.fn();
		render(
			<div className="h-screen w-[320px]">
				<ChatSidebar
					activeConversationId={null}
					conversations={[]}
					loading={false}
					onDeleteConversation={vi.fn()}
					onForkConversation={vi.fn()}
					onNewChat={vi.fn()}
					onSearch={onSearch}
					onSelectConversation={vi.fn()}
				/>
			</div>,
		);

		expect(
			screen.queryByRole("searchbox", { name: "Search conversations" }),
		).not.toBeInTheDocument();
		const searchButton = screen.getByRole("button", { name: "Open conversation search" });
		expect(searchButton).toHaveAttribute("aria-haspopup", "dialog");
		expect(searchButton.querySelector("svg")).toHaveClass("lucide-search");

		fireEvent.click(searchButton);
		expect(onSearch).toHaveBeenCalledOnce();
	});

	it("keeps the composer icon and workspace filtering out of the sidebar", () => {
		render(
			<div className="h-screen w-[320px]">
				<ChatSidebar
					activeConversationId={null}
					conversations={[]}
					loading={false}
					onDeleteConversation={vi.fn()}
					onForkConversation={vi.fn()}
					onNewChat={vi.fn()}
					onSearch={vi.fn()}
					onSelectConversation={vi.fn()}
				/>
			</div>,
		);

		expect(screen.getByTestId("sidebar-new-chat-button").querySelector("svg")).toHaveClass(
			"lucide-square-pen",
		);
		expect(
			screen.queryByLabelText("Filter conversations by workspace"),
		).not.toBeInTheDocument();
	});
});

describe("ConversationSearchDialog", () => {
	it("focuses search and exposes search, workspace, and result actions", async () => {
		const onClose = vi.fn();
		const onCwdFilterChange = vi.fn();
		const onLoadMore = vi.fn();
		const onSearchTermChange = vi.fn();
		const onSelectConversation = vi.fn();

		render(
			<>
				<button data-testid="outside-dialog" type="button">
					Outside dialog
				</button>
				<ConversationSearchDialog
					conversations={[
						{
							id: "conv-a",
							createdAt: "2024-01-01T00:00:00Z",
							updatedAt: "2024-01-01T00:00:00Z",
							messageCount: 1,
							summary: "Alpha conversation",
							cwd: "/workspace/a",
						},
					]}
					cwdFilter=""
					cwdOptions={["/workspace/a", "/workspace/b"]}
					hasMore
					loading={false}
					onClose={onClose}
					onCwdFilterChange={onCwdFilterChange}
					onLoadMore={onLoadMore}
					onSearchTermChange={onSearchTermChange}
					onSelectConversation={onSelectConversation}
					searchTerm="needle"
					total={3}
				/>
			</>,
		);

		const searchInput = screen.getByRole("searchbox", { name: "Search conversations" });
		await waitFor(() => expect(searchInput).toHaveFocus());
		screen.getByTestId("outside-dialog").focus();
		fireEvent.keyDown(window, { key: "Tab" });
		expect(screen.getByRole("button", { name: "Close conversation search" })).toHaveFocus();
		searchInput.focus();
		fireEvent.change(searchInput, {
			target: { value: "updated search" },
		});
		fireEvent.click(screen.getByRole("button", { name: "Clear conversation search" }));
		expect(searchInput).toHaveFocus();
		fireEvent.change(screen.getByLabelText("Search workspace"), {
			target: { value: "/workspace/b" },
		});
		fireEvent.click(screen.getByRole("button", { name: /Alpha conversation/i }));
		fireEvent.click(screen.getByRole("button", { name: "Load more" }));

		expect(onSearchTermChange).toHaveBeenNthCalledWith(1, "updated search");
		expect(onSearchTermChange).toHaveBeenNthCalledWith(2, "");
		expect(onCwdFilterChange).toHaveBeenCalledWith("/workspace/b");
		expect(onSelectConversation).toHaveBeenCalledWith("conv-a");
		expect(onLoadMore).toHaveBeenCalledOnce();
		expect(screen.getByRole("button", { name: /Alpha conversation/i })).toHaveTextContent(
			"conv-a",
		);
		expect(screen.getByText("1 of 3")).toBeInTheDocument();

		fireEvent.keyDown(window, { key: "Escape" });
		expect(onClose).toHaveBeenCalledOnce();
	});

	it("expands the group containing a newly active conversation", async () => {
		const conversations = [
			{
				id: "conv-a",
				createdAt: "2024-01-01T00:00:00Z",
				updatedAt: "2024-01-03T00:00:00Z",
				messageCount: 1,
				summary: "Alpha conversation",
				cwd: "/workspace/a",
			},
			{
				id: "conv-b",
				createdAt: "2024-01-01T00:00:00Z",
				updatedAt: "2024-01-02T00:00:00Z",
				messageCount: 1,
				summary: "Beta conversation",
				cwd: "/workspace/b",
			},
		];
		const props = {
			conversations,
			loading: false,
			onDeleteConversation: vi.fn(),
			onForkConversation: vi.fn(),
			onNewChat: vi.fn(),
			onSearch: vi.fn(),
			onSelectConversation: vi.fn(),
		};
		const { rerender } = render(
			<ChatSidebar activeConversationId={null} {...props} />,
		);

		expect(screen.queryByText("Beta conversation")).not.toBeInTheDocument();

		rerender(<ChatSidebar activeConversationId="conv-b" {...props} />);

		await waitFor(() => expect(screen.getByText("Beta conversation")).toBeInTheDocument());
	});

	it("reports an empty workspace filter without claiming there are no conversations", () => {
		render(
			<ConversationSearchDialog
				conversations={[]}
				cwdFilter="/workspace/missing"
				cwdOptions={["/workspace/missing"]}
				loading={false}
				onClose={vi.fn()}
				onCwdFilterChange={vi.fn()}
				onSearchTermChange={vi.fn()}
				onSelectConversation={vi.fn()}
				searchTerm=""
			/>,
		);

		expect(screen.getByText("No conversations in this workspace.")).toBeInTheDocument();
		expect(screen.queryByText("No saved conversations yet.")).not.toBeInTheDocument();
	});

	it("reports search failures instead of leaving stale results visible", () => {
		render(
			<ConversationSearchDialog
				conversations={[]}
				cwdFilter=""
				cwdOptions={[]}
				error="Search is temporarily unavailable"
				loading={false}
				onClose={vi.fn()}
				onCwdFilterChange={vi.fn()}
				onSearchTermChange={vi.fn()}
				onSelectConversation={vi.fn()}
				searchTerm="needle"
			/>,
		);

		expect(screen.getByRole("alert")).toHaveTextContent(
			"Search is temporarily unavailable",
		);
		expect(screen.queryByText("No conversations match your search.")).not.toBeInTheDocument();
	});
});
