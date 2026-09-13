/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{js,jsx,ts,tsx}'],
  theme: {
    extend: {
      colors: {
        // Ported verbatim from trovo-wallet-monorepo/web/tailwind.config.js
        // (PLAN.md §1.1) so wallet-web matches the existing app's theme.
        primary: {
          100: '#F2F6F9',
          200: '#CCDBE7',
          300: '#99B6CF',
          500: '#99B6CF',
          600: '#6692B8',
          700: '#336DA0',
          800: '#004988',
        },
        trovored: {
          light: '#FFE7E2',
          primary: '#BE3800',
        },
        // New: the original theme has no positive/success color. Added
        // for the online/offline connectivity indicator (PLAN.md §6.4) -
        // trovored already covers the "offline/alert" state.
        positive: {
          light: '#E3F5E9',
          primary: '#1E8E4F',
        },
      },
    },
    fontFamily: {
      matahariRegular: ['MatahariRegular', 'sans-serif'],
      matahariExtended: ['MatahariExtended', 'sans-serif'],
      montserratMedium: ['MontserratMedium', 'sans-serif'],
      montserratRegular: ['MontserratRegular', 'sans-serif'],
      montserratSemiBold: ['MontserratSemiBold', 'sans-serif'],
    },
    fontWeight: {
      bold: '700',
    },
  },
  plugins: [],
};
