; Explorer entries for "添加到 Rakko". mygo runs customInstall once the
; files are in $INSTDIR and customUnInstall before the folder is removed;
; everything here is under HKCU and the app's own key names.

!macro customInstall
  ; 添加到 Rakko on files and folders. Player mode invokes the verb once
  ; with every selected item, so %* carries the whole selection.
  WriteRegStr HKCU "Software\Classes\*\shell\Rakko" "" "添加到 Rakko"
  WriteRegStr HKCU "Software\Classes\*\shell\Rakko" "Icon" '"$INSTDIR\Rakko.exe",0'
  WriteRegStr HKCU "Software\Classes\*\shell\Rakko" "MultiSelectModel" "Player"
  WriteRegStr HKCU "Software\Classes\*\shell\Rakko\command" "" '"$INSTDIR\Rakko.exe" --add-to-rakko %*'
  WriteRegStr HKCU "Software\Classes\Directory\shell\Rakko" "" "添加到 Rakko"
  WriteRegStr HKCU "Software\Classes\Directory\shell\Rakko" "Icon" '"$INSTDIR\Rakko.exe",0'
  WriteRegStr HKCU "Software\Classes\Directory\shell\Rakko" "MultiSelectModel" "Player"
  WriteRegStr HKCU "Software\Classes\Directory\shell\Rakko\command" "" '"$INSTDIR\Rakko.exe" --add-to-rakko %*'

  ; 发送到 entry: SendTo appends the selected paths to the shortcut's
  ; arguments.
  CreateDirectory "$APPDATA\Microsoft\Windows\SendTo"
  CreateShortCut "$APPDATA\Microsoft\Windows\SendTo\Rakko.lnk" "$INSTDIR\Rakko.exe" "--add-to-rakko" "" 0 SW_SHOWNORMAL
!macroend

!macro customUnInstall
  DeleteRegKey HKCU "Software\Classes\*\shell\Rakko"
  DeleteRegKey HKCU "Software\Classes\Directory\shell\Rakko"
  Delete "$APPDATA\Microsoft\Windows\SendTo\Rakko.lnk"
!macroend
