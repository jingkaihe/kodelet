import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { AuthPrincipal } from "../../types";
import ChatSidebar from "./ChatSidebar";

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
