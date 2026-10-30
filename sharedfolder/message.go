package sharedfolder

import (
	"errors"
	"fmt"
)

// Message localizes only a recognized permission error and keeps unrelated
// failures intact. The current kernel check remains the source of truth.
func Message(err error, locale string) string {
	var permission *PermissionError
	if !errors.As(err, &permission) {
		return err.Error()
	}
	need := "RO"
	if permission.Need == Write {
		need = "RW"
	}
	format := "No %s permission on shared folder %q for package account %q; grant %s or reinstall using the dedicated folder"
	switch locale {
	case "cht", "zh-TW":
		format = "共用資料夾 %q 缺少 %s 權限（套件帳號 %q）；請授予 %s，或重新安裝並使用專用資料夾"
	case "chs", "zh-CN":
		format = "共享文件夹 %q 缺少 %s 权限（套件账户 %q）；请授予 %s，或重新安装并使用专用文件夹"
	case "jpn", "ja":
		format = "共有フォルダ %q に %s 権限がありません（パッケージアカウント %q）。%s を付与するか専用フォルダで再インストールしてください"
	case "krn", "ko":
		format = "공유 폴더 %q에 %s 권한이 없습니다 (패키지 계정 %q). %s 권한을 부여하거나 전용 폴더로 다시 설치하세요"
	case "ger", "de":
		format = "Dem gemeinsamen Ordner %q fehlen %s-Rechte für Paketkonto %q; gewähren Sie %s oder installieren Sie mit eigenem Ordner neu"
	case "fre", "fr":
		format = "Le dossier partagé %q n’a pas les droits %s pour le compte du paquet %q ; accordez %s ou réinstallez avec un dossier dédié"
	case "spn", "es":
		format = "La carpeta compartida %q carece de permisos %s para la cuenta del paquete %q; conceda %s o reinstale con una carpeta dedicada"
	case "ita", "it":
		format = "La cartella condivisa %q non ha i permessi %s per l’account del pacchetto %q; concedi %s o reinstalla con una cartella dedicata"
	default:
		return fmt.Sprintf(format, need, permission.Name, permission.Account, need)
	}
	return fmt.Sprintf(format, permission.Name, need, permission.Account, need)
}
