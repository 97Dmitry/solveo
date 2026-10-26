import type { ReactNode } from "react";
import { ErrorBoundary, type FallbackProps } from "react-error-boundary";
import { Button } from "../ui/Button";
import type { Reporter } from "./report";

function Fallback({ resetErrorBoundary }: FallbackProps) {
  return (
    <div className="render-failure" role="alert">
      <h2>Ошибка интерфейса</h2>
      <p>
        Окно не смогло отобразиться. Чаты и расшифровки не потеряны, сведения об ошибке записаны в
        журнал диагностики.
      </p>
      <div className="button-row">
        <Button className="primary" onClick={resetErrorBoundary}>
          Попробовать снова
        </Button>
        <Button onClick={() => window.location.reload()}>Перезагрузить окно</Button>
      </div>
    </div>
  );
}

// RenderBoundary reports React render failures and offers recovery instead of a blank window.
export function RenderBoundary({ report, children }: { report: Reporter; children: ReactNode }) {
  return (
    <ErrorBoundary
      FallbackComponent={Fallback}
      onError={(error, info) => report("render", error, info.componentStack ?? "")}
    >
      {children}
    </ErrorBoundary>
  );
}
