import { render, screen } from "@testing-library/react";

import { AccessLandingApp } from "./AccessLandingPage";

vi.mock("../auth/api", () => ({
  refreshSession: vi.fn().mockResolvedValue(null),
  logout: vi.fn(),
}));
vi.mock("./NotificationBell", () => ({ NotificationBell: () => null }));

it("renders an invitation outside the editor and preserves the login return route", async () => {
  render(
    <AccessLandingApp
      kind="invite"
      initialToken="test-invitation-token"
      invitationId=""
    />,
  );
  expect(await screen.findByText(/Sign in or register/)).toBeVisible();
  expect(screen.getByRole("link", { name: "Sign in" })).toHaveAttribute(
    "href",
    expect.stringContaining("returnTo="),
  );
});
