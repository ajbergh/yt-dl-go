Unicode true
!include "MUI2.nsh"
!include "LogicLib.nsh"
!include "x64.nsh"
Name "YouTube Vault"
OutFile "${OUTPUT}"
InstallDir "$LOCALAPPDATA\Programs\YouTube Vault"
InstallDirRegKey HKCU "Software\YouTube Vault" "InstallDir"
RequestExecutionLevel user
SetCompressor /SOLID lzma
Icon "${ICON}"
UninstallIcon "${ICON}"
VIProductVersion "${VERSION}.0"
VIAddVersionKey "ProductName" "YouTube Vault"
VIAddVersionKey "FileDescription" "YouTube Vault Setup"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "LegalCopyright" "YouTube Vault contributors"
!define KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\YouTube Vault"
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_LICENSE "${PAYLOAD}\LICENSE"
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

Function .onInit
  SetRegView 64
  SetShellVarContext current
!if "${ARCH}" == "arm64"
  ${IfNot} ${IsNativeARM64}
!else
  ${IfNot} ${IsNativeAMD64}
!endif
    MessageBox MB_ICONSTOP "Please download the installer for your computer's architecture." /SD IDOK
    SetErrorLevel 1
    Quit
  ${EndIf}
FunctionEnd

Section "YouTube Vault"
  ; The bootstrapper is shipped by the pinned Wails module and chooses the
  ; device's architecture. It installs per user without elevation.
  ReadRegStr $0 HKLM "SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
  ${If} $0 == ""
    ReadRegStr $0 HKCU "Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
  ${EndIf}
  ${If} $0 == ""
    InitPluginsDir
    SetOutPath "$PLUGINSDIR"
    File /oname=WebView2Setup.exe "${WEBVIEW}"
    ExecWait '"$PLUGINSDIR\WebView2Setup.exe" /silent /install' $0
    ${If} $0 != 0
      MessageBox MB_ICONSTOP "WebView2 could not be installed. Check your internet connection and try again." /SD IDOK
      SetErrorLevel 1
      Abort
    ${EndIf}
  ${EndIf}
  SetOutPath "$INSTDIR\app"
  SetOverwrite on
  ClearErrors
  File /r "${PAYLOAD}\*.*"
  ${If} ${Errors}
    SetErrorLevel 1
    Abort "Close YouTube Vault and try again."
  ${EndIf}
  WriteUninstaller "$INSTDIR\uninstall.exe"
  CreateShortcut "$SMPROGRAMS\YouTube Vault.lnk" "$INSTDIR\app\youtube-vault.exe"
  CreateShortcut "$DESKTOP\YouTube Vault.lnk" "$INSTDIR\app\youtube-vault.exe"
  WriteRegStr HKCU "Software\YouTube Vault" "InstallDir" "$INSTDIR"
  WriteRegStr HKCU "${KEY}" "DisplayName" "YouTube Vault"
  WriteRegStr HKCU "${KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${KEY}" "Publisher" "YouTube Vault contributors"
  WriteRegStr HKCU "${KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${KEY}" "DisplayIcon" "$INSTDIR\app\youtube-vault.exe"
  WriteRegStr HKCU "${KEY}" "UninstallString" '$\"$INSTDIR\uninstall.exe$\"'
  WriteRegStr HKCU "${KEY}" "QuietUninstallString" '$\"$INSTDIR\uninstall.exe$\" /S'
  WriteRegDWORD HKCU "${KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${KEY}" "NoRepair" 1
SectionEnd

Section "Uninstall"
  SetRegView 64
  SetShellVarContext current
  ; Only the owned payload is recursive. Never remove profile data, downloads,
  ; WebView data, or unrelated files in a user-selected installation directory.
  RMDir /r "$INSTDIR\app"
  Delete "$INSTDIR\uninstall.exe"
  Delete "$SMPROGRAMS\YouTube Vault.lnk"
  Delete "$DESKTOP\YouTube Vault.lnk"
  DeleteRegKey HKCU "${KEY}"
  DeleteRegKey HKCU "Software\YouTube Vault"
  RMDir "$INSTDIR"
SectionEnd
