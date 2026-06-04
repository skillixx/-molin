package crypto

import "golang.org/x/crypto/bcrypt"

// HashPassword 使用 bcrypt(cost=12) 对密码做哈希。
func HashPassword(plain string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), 12)
	return string(hash), err
}

// CheckPassword 验证密码是否与 hash 匹配。
func CheckPassword(plain, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}
