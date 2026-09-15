// Mirrors wallet-web's store/walletSlice.ts: holds ONLY public addresses
// and booleans (has-a-vault, is-unlocked) - never a mnemonic, password,
// or private key. The primary wallet is a Safe with no private key of
// its own (wallet-backend PLAN.md §13) - "vault"/"unlocked" for it just
// means "we know its address," which is trivially always true once known
// (see OnboardingWizard's finishWithPrimaryWallet), unlike signer, which
// has a real encrypted vault and needs an explicit unlock step.
import 'package:flutter/foundation.dart';

class RoleState {
  const RoleState({this.address, this.hasVault = false, this.isUnlocked = false});
  final String? address;
  final bool hasVault;
  final bool isUnlocked;

  RoleState copyWith({String? address, bool? hasVault, bool? isUnlocked}) => RoleState(
        address: address ?? this.address,
        hasVault: hasVault ?? this.hasVault,
        isUnlocked: isUnlocked ?? this.isUnlocked,
      );
}

class WalletState extends ChangeNotifier {
  RoleState _signer = const RoleState();
  RoleState _primary = const RoleState();
  String? _username;

  RoleState get signer => _signer;
  RoleState get primary => _primary;
  String? get username => _username;

  bool get onboardingComplete => _signer.hasVault && _primary.hasVault;
  bool get isFullyUnlocked => _signer.isUnlocked && _primary.isUnlocked;

  void hydrate({required RoleState signer, required RoleState primary}) {
    _signer = signer;
    _primary = primary;
    notifyListeners();
  }

  void vaultCreated({required String role, required String address}) {
    final state = RoleState(address: address, hasVault: true, isUnlocked: true);
    if (role == 'signer') {
      _signer = state;
    } else {
      _primary = state;
    }
    notifyListeners();
  }

  void unlocked({required String role, required String address}) {
    if (role == 'signer') {
      _signer = _signer.copyWith(address: address, isUnlocked: true);
    } else {
      _primary = _primary.copyWith(address: address, isUnlocked: true);
    }
    notifyListeners();
  }

  void lockedAll() {
    _signer = _signer.copyWith(isUnlocked: false);
    _primary = _primary.copyWith(isUnlocked: false);
    notifyListeners();
  }

  void walletRemoved({required String role}) {
    if (role == 'signer') {
      _signer = const RoleState();
    } else {
      _primary = const RoleState();
    }
    notifyListeners();
  }

  void profileRegistered(String? username) {
    _username = username;
    notifyListeners();
  }

  void signedOut() {
    _username = null;
    notifyListeners();
  }
}
