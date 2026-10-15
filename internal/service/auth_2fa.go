package service

import (
	"crypto/subtle"
	"errors"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"topup-backend/internal/domain"
	"topup-backend/internal/pkg/crypto"
)

const (
	maxFails = 5
	lockTime = 15 * time.Minute
)

func matchTOTP(secret, code string) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return 0, false
	}
	cur := time.Now().Unix() / 30
	for _, off := range []int64{0, -1, 1} {
		step := cur + off
		exp, err := totp.GenerateCodeCustom(secret, time.Unix(step*30, 0), totp.ValidateOpts{
			Period: 30, Skew: 0, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
		})
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(exp), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}

func (s *authService) checkTOTP(user *domain.User, code string) error {
	if user.TwoFALockedUntil != nil && time.Now().Before(*user.TwoFALockedUntil) {
		return errors.New("terlalu banyak percobaan, coba lagi nanti")
	}
	secret, err := crypto.DecryptString(user.TOTPSecret, s.cfg.TOTPEncKey)
	if err != nil || secret == "" {
		return errors.New("2FA belum diatur")
	}

	step, ok := matchTOTP(secret, code)
	if !ok || step <= user.TOTPLastStep {
		fails := user.TwoFAFails + 1
		upd := map[string]interface{}{"TwoFAFails": fails}
		if fails >= maxFails {
			upd["TwoFAFails"] = 0
			upd["TwoFALockedUntil"] = time.Now().Add(lockTime)
		}
		_ = s.userRepo.UpdateFields(user.ID, upd)
		return errors.New("kode verifikasi salah")
	}

	user.TOTPLastStep = step
	return s.userRepo.UpdateFields(user.ID, map[string]interface{}{
		"TOTPLastStep":     step,
		"TwoFAFails":       0,
		"TwoFALockedUntil": nil,
	})
}

func (s *authService) Verify2FALogin(tempToken, code string) (*domain.User, string, error) {
	claims, err := s.parseToken(tempToken)
	if err != nil || claims.Purpose != purpose2FA {
		return nil, "", errors.New("sesi login kedaluwarsa, silakan login ulang")
	}
	user, err := s.userRepo.FindByID(claims.UserID)
	if err != nil || !user.IsActive || !user.TOTPEnabled {
		return nil, "", errors.New("sesi login tidak valid")
	}
	if err := s.checkTOTP(user, code); err != nil {
		return nil, "", err
	}
	token, err := s.generateJWT(user)
	if err != nil {
		return nil, "", err
	}
	return user, token, nil
}

// Secret 16 karakter Base32 (10 byte). Belum aktif sampai dikonfirmasi.
func (s *authService) Setup2FA(userID uint) (string, error) {
	user, err := s.userRepo.FindByID(userID)
	if err != nil {
		return "", err
	}
	if user.TOTPEnabled {
		return "", errors.New("2FA sudah aktif")
	}
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer: "IRXPLAY", AccountName: user.Email, SecretSize: 10,
	})
	if err != nil {
		return "", err
	}
	enc, err := crypto.EncryptString(key.Secret(), s.cfg.TOTPEncKey)
	if err != nil {
		return "", err
	}
	if err := s.userRepo.UpdateFields(user.ID, map[string]interface{}{
		"TOTPSecret":       enc,
		"TOTPLastStep":     0,
		"TwoFAFails":       0,
		"TwoFALockedUntil": nil,
	}); err != nil {
		return "", err
	}
	return key.Secret(), nil
}

func (s *authService) Enable2FA(userID uint, code string) error {
	user, err := s.userRepo.FindByID(userID)
	if err != nil {
		return err
	}
	if user.TOTPEnabled || user.TOTPSecret == "" {
		return errors.New("jalankan setup 2FA terlebih dahulu")
	}
	if err := s.checkTOTP(user, code); err != nil {
		return err
	}
	return s.userRepo.UpdateFields(user.ID, map[string]interface{}{"TOTPEnabled": true})
}

func (s *authService) Disable2FA(userID uint, password, code string) error {
	user, err := s.userRepo.FindByID(userID)
	if err != nil {
		return err
	}
	if !user.TOTPEnabled {
		return errors.New("2FA belum aktif")
	}
	if !crypto.CheckPasswordHash(password, user.Password) {
		return errors.New("password salah")
	}
	if err := s.checkTOTP(user, code); err != nil {
		return err
	}
	return s.userRepo.UpdateFields(user.ID, map[string]interface{}{
		"TOTPEnabled":  false,
		"TOTPSecret":   "",
		"TOTPLastStep": 0,
	})
}
