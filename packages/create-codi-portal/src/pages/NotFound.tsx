import { useT } from "../i18n/index.tsx";
import { CenteredMessage } from "../portal.tsx";
import { Link } from "../router.tsx";

export function NotFound() {
  const t = useT();
  return (
    <CenteredMessage title={t.notFound.title}>
      <p>{t.notFound.text}</p>
      <Link to="/" className="codi-button codi-button--secondary codi-button--md">
        <span className="codi-button__label">{t.notFound.home}</span>
      </Link>
    </CenteredMessage>
  );
}
