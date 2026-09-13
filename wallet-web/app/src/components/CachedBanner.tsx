type Props = {
  asOf: number | null;
};

// PLAN.md §6.4: offline-rendered data is always timestamped so it's
// never mistaken for live data.
export default function CachedBanner({ asOf }: Props) {
  if (!asOf) return null;
  return (
    <div className="bg-primary-100 text-primary-700 text-sm rounded-md px-3 py-2 font-montserratMedium">
      Showing cached data from {new Date(asOf).toLocaleString()} - reconnect to refresh.
    </div>
  );
}
