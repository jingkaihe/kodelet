export default function KodeletBrand({ label = 'Kodelet' }: { label?: string }) {
  return (
    <div className="kodelet-brand" role="img" aria-label={label}>
      <span aria-hidden="true">
        kodelet<span className="kodelet-brand-dot">.</span>
      </span>
    </div>
  );
}
