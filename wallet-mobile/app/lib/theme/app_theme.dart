// Ported verbatim from wallet-web's tailwind.config.js (itself ported
// from trovo-wallet-monorepo/web/tailwind.config.js, PLAN.md §1.1) - the
// same color/font tokens, so this app and wallet-web read as the same
// product on two platforms.
import 'package:flutter/material.dart';

class AppColors {
  AppColors._();

  static const primary100 = Color(0xFFF2F6F9);
  static const primary200 = Color(0xFFCCDBE7);
  static const primary300 = Color(0xFF99B6CF);
  static const primary500 = Color(0xFF99B6CF);
  static const primary600 = Color(0xFF6692B8);
  static const primary700 = Color(0xFF336DA0);
  static const primary800 = Color(0xFF004988);

  static const trovoredLight = Color(0xFFFFE7E2);
  static const trovoredPrimary = Color(0xFFBE3800);

  // New here too, matching wallet-web's own addition for the
  // online/offline connectivity indicator (PLAN.md §6.4) - the original
  // theme has no positive/success color.
  static const positiveLight = Color(0xFFE3F5E9);
  static const positivePrimary = Color(0xFF1E8E4F);

  static const gray200 = Color(0xFFE5E7EB);
  static const gray300 = Color(0xFFD1D5DB);
  static const gray600 = Color(0xFF4B5563);
  static const gray700 = Color(0xFF374151);
}

class AppFonts {
  AppFonts._();

  static const matahariRegular = 'MatahariRegular';
  static const matahariExtended = 'MatahariExtended';
  static const montserratRegular = 'MontserratRegular';
  static const montserratMedium = 'MontserratMedium';
  static const montserratSemiBold = 'MontserratSemiBold';
}

ThemeData buildAppTheme() {
  return ThemeData(
    useMaterial3: true,
    scaffoldBackgroundColor: Colors.white,
    fontFamily: AppFonts.montserratRegular,
    colorScheme: ColorScheme.fromSeed(
      seedColor: AppColors.primary800,
      primary: AppColors.primary800,
      error: AppColors.trovoredPrimary,
    ),
    textTheme: const TextTheme(
      headlineMedium: TextStyle(fontFamily: AppFonts.matahariExtended, color: AppColors.primary800),
      titleMedium: TextStyle(fontFamily: AppFonts.montserratSemiBold, color: AppColors.primary800),
      bodyMedium: TextStyle(fontFamily: AppFonts.montserratRegular, color: AppColors.gray700),
      labelLarge: TextStyle(fontFamily: AppFonts.montserratMedium, color: AppColors.primary700),
    ),
    elevatedButtonTheme: ElevatedButtonThemeData(
      style: ElevatedButton.styleFrom(
        backgroundColor: AppColors.primary800,
        foregroundColor: Colors.white,
        minimumSize: const Size.fromHeight(48),
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(8)),
        textStyle: const TextStyle(fontFamily: AppFonts.montserratMedium),
      ),
    ),
    outlinedButtonTheme: OutlinedButtonThemeData(
      style: OutlinedButton.styleFrom(
        foregroundColor: AppColors.primary800,
        side: const BorderSide(color: AppColors.primary800),
        minimumSize: const Size.fromHeight(48),
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(8)),
        textStyle: const TextStyle(fontFamily: AppFonts.montserratMedium),
      ),
    ),
    inputDecorationTheme: InputDecorationTheme(
      border: OutlineInputBorder(borderRadius: BorderRadius.circular(8), borderSide: const BorderSide(color: AppColors.gray200)),
      enabledBorder:
          OutlineInputBorder(borderRadius: BorderRadius.circular(8), borderSide: const BorderSide(color: AppColors.gray200)),
      focusedBorder:
          OutlineInputBorder(borderRadius: BorderRadius.circular(8), borderSide: const BorderSide(color: AppColors.primary600, width: 2)),
      labelStyle: const TextStyle(fontFamily: AppFonts.montserratMedium, color: AppColors.primary700),
    ),
  );
}
