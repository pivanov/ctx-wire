import { Component, type ErrorInfo, type ReactNode } from "react";

// Defense-in-depth: a render error in a telemetry-driven section (e.g. a
// malformed /v1/impact payload) must not blank the whole page. Wrap each such
// section so a crash is contained to that section and the rest of the site
// keeps working. The primary guard is Object.hasOwn in the consumers; this is
// the belt-and-suspenders layer.
export class ErrorBoundary extends Component<
  { children: ReactNode; fallback?: ReactNode },
  { hasError: boolean }
> {
  state = { hasError: false };

  static getDerivedStateFromError(): { hasError: boolean } {
    return { hasError: true };
  }

  componentDidCatch(error: Error, info: ErrorInfo): void {
    console.error("ctx-wire ui error boundary:", error, info);
  }

  render(): ReactNode {
    if (this.state.hasError) {
      return this.props.fallback ?? null;
    }
    return this.props.children;
  }
}
