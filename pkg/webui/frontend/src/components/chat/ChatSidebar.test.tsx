import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { AuthPrincipal } from "../../types";
import ChatSidebar, { ConversationSearchDialog } from "./ChatSidebar";

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
		const onSearchTermChange = vi.fn();
		const onSelectConversation = vi.fn();

		render(
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
				loading={false}
				onClose={onClose}
				onCwdFilterChange={onCwdFilterChange}
				onSearchTermChange={onSearchTermChange}
				onSelectConversation={onSelectConversation}
				searchTerm="needle"
			/>,
		);

		const searchInput = screen.getByRole("searchbox", { name: "Search conversations" });
		await waitFor(() => expect(searchInput).toHaveFocus());
		fireEvent.change(searchInput, {
			target: { value: "updated search" },
		});
		fireEvent.click(screen.getByRole("button", { name: "Clear conversation search" }));
		fireEvent.change(screen.getByLabelText("Search workspace"), {
			target: { value: "/workspace/b" },
		});
		fireEvent.click(screen.getByRole("button", { name: /Alpha conversation/i }));

		expect(onSearchTermChange).toHaveBeenNthCalledWith(1, "updated search");
		expect(onSearchTermChange).toHaveBeenNthCalledWith(2, "");
		expect(onCwdFilterChange).toHaveBeenCalledWith("/workspace/b");
		expect(onSelectConversation).toHaveBeenCalledWith("conv-a");

		fireEvent.keyDown(window, { key: "Escape" });
		expect(onClose).toHaveBeenCalledOnce();
	});
});
