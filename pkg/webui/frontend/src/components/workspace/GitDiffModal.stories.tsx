import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn } from "storybook/test";
import GitDiffModal from "./GitDiffModal";
import { sampleGitDiff } from "../../stories/fixtures";

const meta = {
	title: "Workspace/GitDiffModal",
	component: GitDiffModal,
	parameters: {
		layout: "fullscreen",
	},
	args: {
		cwdLabel: "/home/jingkaihe/workspace/kodelet",
		error: null,
		gitDiff: sampleGitDiff,
		loading: false,
		open: true,
		onClose: fn(),
		onRefresh: fn(),
	},
} satisfies Meta<typeof GitDiffModal>;

export default meta;

type Story = StoryObj<typeof meta>;

export const WithDiff: Story = {};

export const Loading: Story = {
	args: {
		gitDiff: null,
		loading: true,
	},
};

export const Empty: Story = {
	args: {
		gitDiff: {
			...sampleGitDiff,
			diff: "",
			has_diff: false,
		},
	},
};

export const ErrorState: Story = {
	args: {
		error: "Not a git repository",
		gitDiff: null,
	},
};
