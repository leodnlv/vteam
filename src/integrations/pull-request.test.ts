import { describe, it, expect } from "vitest";
import { gitHubReviewStatus, gitLabReviewStatus } from "./pull-request.js";

describe("gitHubReviewStatus", () => {
  it.each([
    ["APPROVED", "approved"],
    ["CHANGES_REQUESTED", "changes_requested"],
    ["REVIEW_REQUIRED", "review_required"],
  ] as const)("uses reviewDecision %s when the branch requires reviews", (decision, expected) => {
    expect(gitHubReviewStatus(decision, [{ state: "APPROVED" }])).toBe(expected);
  });

  it("falls back to the latest reviews when no review is required", () => {
    expect(gitHubReviewStatus("", [{ state: "APPROVED" }])).toBe("approved");
    expect(gitHubReviewStatus(null, [{ state: "APPROVED" }, { state: "CHANGES_REQUESTED" }])).toBe("changes_requested");
    expect(gitHubReviewStatus("", [{ state: "COMMENTED" }])).toBe("none");
    expect(gitHubReviewStatus("", [])).toBe("none");
  });
});

describe("gitLabReviewStatus", () => {
  it("reports requested changes first", () => {
    expect(gitLabReviewStatus("requested_changes", { approved: true, approved_by: [{}] })).toBe("changes_requested");
  });

  it("is approved only when someone approved and the rules are met", () => {
    expect(gitLabReviewStatus("mergeable", { approved: true, approved_by: [{}] })).toBe("approved");
    expect(gitLabReviewStatus("not_approved", { approved: false, approved_by: [{}] })).toBe("review_required");
  });

  it("distinguishes missing required approvals from no reviews at all", () => {
    expect(gitLabReviewStatus("not_approved", { approved: false, approved_by: [] })).toBe("review_required");
    expect(gitLabReviewStatus("mergeable", { approved: true, approved_by: [] })).toBe("none");
  });
});
