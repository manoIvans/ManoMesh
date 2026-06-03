package domain

import (
	"errors"
	"time"
)

// ErrUserNotFound é retornado pelo repositório quando o usuário
// procurado não existe. O handler usa este sentinel para diferenciar
// "credencial inválida" (401) de "erro de banco" (500).
var ErrUserNotFound = errors.New("usuário não encontrado")

// ErrEmailAlreadyExists é retornado ao tentar criar um usuário com
// email já cadastrado. Mapeado para HTTP 409 no handler.
var ErrEmailAlreadyExists = errors.New("email já cadastrado")

// ErrUsernameAlreadyExists é retornado ao tentar criar/atualizar com
// um username já em uso. Mapeado para 409. Distinto de email pra que
// a UI possa apontar pro campo certo no form.
var ErrUsernameAlreadyExists = errors.New("username já cadastrado")

// ErrInvalidToken: token de reset/verificação não existe no DB OU já
// foi usado OU não bate. Backend mistura os 3 casos no mesmo sentinel
// pra não vazar info útil pra atacante (qual passo do fluxo falhou).
// Mapeado pra 400.
var ErrInvalidToken = errors.New("token inválido")

// ErrExpiredToken: token existe E não foi usado, mas expires_at < now.
// Mapeado pra 410 Gone — o recurso existiu mas não está mais utilizável,
// cliente deve solicitar um novo.
var ErrExpiredToken = errors.New("token expirado")

// ErrRefreshTokenReused: detecção de reuso na rotação estrita —
// tentativa de usar token já revogado E com replaced_by_id setado.
// Reação no handler: revogar TUDO do user. Mapeado pra 401 +
// derrubar todas as sessões.
var ErrRefreshTokenReused = errors.New("refresh token reusado (possível roubo)")

// ErrEmailNotVerified: usuário tenta ação que exige email verificado
// (ex: POST /assets). Mapeado pra 403. Frontend mostra banner +
// botão "reenviar verificação".
var ErrEmailNotVerified = errors.New("email não verificado")

// User representa um usuário persistido no banco.
//
// PasswordHash NUNCA deve ser serializado em respostas HTTP — daí o
// `json:"-"`. Erros de cópia/paste que vazam o hash são uma classe
// inteira de bug que essa tag previne.
//
// AvatarPath é nullable: usuários começam sem avatar e a UI exibe um
// placeholder. Usa *string em vez de sql.NullString pra que o JSON
// resulte em `"avatar_path": null` ou ausência, e não em
// `{"String":"","Valid":false}`.
type User struct {
	ID              int64      `json:"id"`
	Email           string     `json:"email"`
	Username        string     `json:"username"`
	DisplayName     string     `json:"display_name"`
	Bio             string     `json:"bio"`
	AvatarPath      *string    `json:"avatar_path,omitempty"`
	PasswordHash    string     `json:"-"`
	EmailVerifiedAt *time.Time `json:"email_verified_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// PublicUser é a versão SEM email do User, devolvida em endpoints
// públicos (GET /users/:username, GET /users). Email é dado pessoal;
// mesmo signaling "esse handle existe" é OK porque os assets do
// dono já expõem o vínculo de qualquer jeito.
//
// AssetCount é opcional + omitempty: alguns endpoints (perfil
// individual) não precisam computar a contagem; outros (diretório
// /users) populam pra que o frontend mostre "12 assets" no card.
type PublicUser struct {
	ID          int64     `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	Bio         string    `json:"bio"`
	AvatarPath  *string   `json:"avatar_path,omitempty"`
	AssetCount  *int64    `json:"asset_count,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// ToPublic descarta os campos privados (email, password_hash, updated_at).
// Centralizar a conversão evita "esqueci de tirar o email" em algum
// handler novo.
func (u *User) ToPublic() *PublicUser {
	return &PublicUser{
		ID:          u.ID,
		Username:    u.Username,
		DisplayName: u.DisplayName,
		Bio:         u.Bio,
		AvatarPath:  u.AvatarPath,
		CreatedAt:   u.CreatedAt,
	}
}
