$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName UIAutomationClient, UIAutomationTypes, System.Windows.Forms
Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
using System.Text;

public static class CreatorTestWindows {
    private delegate bool EnumWindow(IntPtr handle, IntPtr data);
    [DllImport("user32.dll")] private static extern bool EnumWindows(EnumWindow callback, IntPtr data);
    [DllImport("user32.dll")] private static extern bool IsWindowVisible(IntPtr handle);
    [DllImport("user32.dll")] private static extern uint GetWindowThreadProcessId(IntPtr handle, out uint processId);
    [DllImport("user32.dll", CharSet = CharSet.Unicode)]
    private static extern int GetWindowText(IntPtr handle, StringBuilder text, int capacity);
    [DllImport("user32.dll")] private static extern bool SetForegroundWindow(IntPtr handle);
    [DllImport("user32.dll")] private static extern IntPtr GetForegroundWindow();
    [DllImport("user32.dll")] private static extern bool AttachThreadInput(uint caller, uint other, bool attach);
    [DllImport("kernel32.dll")] private static extern uint GetCurrentThreadId();
    [DllImport("user32.dll")] public static extern bool ShowWindowAsync(IntPtr handle, int command);
    [DllImport("user32.dll")] public static extern bool IsIconic(IntPtr handle);

    public static bool FocusWindow(IntPtr handle) {
        if (GetForegroundWindow() == handle) return true;
        SetForegroundWindow(handle);
        IntPtr foreground = GetForegroundWindow();
        if (foreground == handle) return true;
        if (foreground == IntPtr.Zero) return false;
        uint owner;
        uint thread = GetWindowThreadProcessId(foreground, out owner);
        uint caller = GetCurrentThreadId();
        // The test driver may not own the foreground input queue when a
        // window opens. Join it just for activation, then send to the test window.
        if (thread == 0 || thread == caller || !AttachThreadInput(caller, thread, true)) return false;
        try {
            SetForegroundWindow(handle);
        } finally {
            AttachThreadInput(caller, thread, false);
        }
        return GetForegroundWindow() == handle;
    }

    public static IntPtr Find(uint processId, string title) {
        IntPtr result = IntPtr.Zero;
        var text = new StringBuilder(128);
        EnumWindows(delegate(IntPtr handle, IntPtr data) {
            uint owner;
            GetWindowThreadProcessId(handle, out owner);
            if (owner != processId || !IsWindowVisible(handle)) return true;
            GetWindowText(handle, text, text.Capacity);
            if (text.ToString() != title) return true;
            result = handle;
            return false;
        }, IntPtr.Zero);
        return result;
    }
}
'@

$creatorPid = [uint32]$env:CREATOR_TEST_PID
$version = $env:CREATOR_TEST_VERSION
$widthLabel = '默认宽度（像素）'

function Wait-For([string]$description, [scriptblock]$probe) {
    $clock = [Diagnostics.Stopwatch]::StartNew()
    do {
        try {
            $result = & $probe
            if ($result) { return $result }
        } catch [System.Windows.Automation.ElementNotAvailableException] {
            # An element in the previous frame may disappear during the action.
        }
        Start-Sleep -Milliseconds 75
    } while ($clock.Elapsed.TotalSeconds -lt 6)
    throw "Timed out waiting for $description"
}

function Open-Window([string]$title) {
    Wait-For "the '$title' window" {
        $handle = [CreatorTestWindows]::Find($creatorPid, $title)
        if ($handle -ne [IntPtr]::Zero) {
            [System.Windows.Automation.AutomationElement]::FromHandle($handle)
        }
    }
}

function Wait-WindowClosed([string]$title) {
    [void](Wait-For "'$title' to close" { [CreatorTestWindows]::Find($creatorPid, $title) -eq [IntPtr]::Zero })
}

function Find-Control($window, [string]$name) {
    $condition = [System.Windows.Automation.PropertyCondition]::new(
        [System.Windows.Automation.AutomationElement]::NameProperty, $name)
    Wait-For "the '$name' control" {
        $window.FindFirst([System.Windows.Automation.TreeScope]::Descendants, $condition)
    }
}

function Invoke-Control($window, [string]$name) {
    (Find-Control $window $name).GetCurrentPattern([System.Windows.Automation.InvokePattern]::Pattern).Invoke()
}

function Expand-Control($window, [string]$name) {
    (Find-Control $window $name).GetCurrentPattern([System.Windows.Automation.ExpandCollapsePattern]::Pattern).Expand()
}

function Select-Control($window, [string]$name) {
    (Find-Control $window $name).GetCurrentPattern([System.Windows.Automation.SelectionItemPattern]::Pattern).Select()
}

function Field($window, [string]$name) {
    (Find-Control $window $name).GetCurrentPattern([System.Windows.Automation.ValuePattern]::Pattern)
}

function Expect-Value($window, [string]$name, [string]$expected) {
    $actual = (Field $window $name).Current.Value
    if ($actual -cne $expected) { throw "'$name' contains '$actual', expected '$expected'" }
}

function Send-WindowKeys($window, [string]$keys) {
    $handle = [IntPtr]$window.Current.NativeWindowHandle
    $title = $window.Current.Name
    [void](Wait-For "keyboard focus in '$title' for '$keys'" { [CreatorTestWindows]::FocusWindow($handle) })
    [System.Windows.Forms.SendKeys]::SendWait($keys)
}

$manager = Open-Window 'Creator'
if ($env:CREATOR_TEST_RESTARTED -eq 'true') {
    $firstApp = Find-Control $manager '创建第一个应用'
    [void](Wait-For 'keyboard focus on the first creation action' { $firstApp.Current.HasKeyboardFocus })
    Send-WindowKeys $manager '{ENTER}'
    $search = Find-Control $manager '网址或应用名称'
    [void](Wait-For 'keyboard focus on the address input' { $search.Current.HasKeyboardFocus })
    (Field $manager '网址或应用名称').SetValue('推理 DeepSeek')
    [void](Find-Control $manager '选择 DeepSeek')
    (Find-Control $manager '网址或应用名称').SetFocus()
    Send-WindowKeys $manager '{ENTER}'
    Expect-Value $manager '应用网址' 'https://chat.deepseek.com'
    Expand-Control $manager '窗口尺寸'
    Expect-Value $manager '窗口宽度（像素）' '960'
    Invoke-Control $manager '取消'
    Send-WindowKeys $manager '^,'
    $panel = Open-Window '设置'
    Expect-Value $panel $widthLabel '960'
    Invoke-Control $panel '恢复默认'
    Invoke-Control $panel '保存'
    Wait-WindowClosed '设置'
    Write-Output 'Restart preserved the library startup page and the default dimensions. Combined search opened DeepSeek from the keyboard. Reset saved the defaults.'
} else {
    $searchLabel = '网址或应用名称'
    $search = Find-Control $manager $searchLabel
    [void](Wait-For 'keyboard focus on the address input' { $search.Current.HasKeyboardFocus })
    Send-WindowKeys $manager 'https://chat.deepseek.com'
    [void](Find-Control $manager '使用网址 DeepSeek')
    Expect-Value $manager $searchLabel 'https://chat.deepseek.com'
    Invoke-Control $manager '清除搜索'
    [void](Find-Control $manager '添加')
    Expect-Value $manager $searchLabel ''

    Expand-Control $manager 'Creator 菜单'
    Invoke-Control $manager '设置…'
    $panel = Open-Window '设置'
    Expect-Value $panel $widthLabel '1200'
    (Field $panel $widthLabel).SetValue('1000')
    Send-WindowKeys $panel '{ESC}'
    Wait-WindowClosed '设置'

    Invoke-Control $manager '选择 DeepSeek'
    $draft = 'Unsaved workspace'
    (Field $manager '应用名称').SetValue($draft)
    $originalWindow = [IntPtr]$manager.Current.NativeWindowHandle
    [void][CreatorTestWindows]::ShowWindowAsync($originalWindow, 6) # SW_MINIMIZE
    [void](Wait-For 'Creator to minimize' { [CreatorTestWindows]::IsIconic($originalWindow) })
    $second = Start-Process -FilePath $env:CREATOR_TEST_ENTRY -PassThru
    if ($second -and $second.Id -ne $creatorPid -and !$second.WaitForExit(5000)) {
        $second.Kill()
        throw 'The second Creator launch did not hand off to the original process'
    }
    [void](Wait-For 'the original Creator window to return' {
        [CreatorTestWindows]::Find($creatorPid, 'Creator') -eq $originalWindow -and
        ![CreatorTestWindows]::IsIconic($originalWindow)
    })
    Expect-Value $manager '应用名称' $draft
    Send-WindowKeys $manager '^,'
    $panel = Open-Window '设置'
    Expect-Value $panel $widthLabel '1200'
    Select-Control $panel '深色'
    Select-Control $panel '应用库'
    (Field $panel $widthLabel).SetValue('960')
    Invoke-Control $panel '保存'
    Wait-WindowClosed '设置'
    Expect-Value $manager '应用名称' $draft
    Invoke-Control $manager '取消'

    Expand-Control $manager 'Creator 菜单'
    Invoke-Control $manager '关于 Creator'
    $about = Open-Window '关于 Creator'
    [void](Find-Control $about "版本 $version")
    Invoke-Control $about 'MIT 许可证'
    $license = (Field $about 'MIT 许可证正文').Current
    if (!$license.IsReadOnly -or $license.Value.Replace("`r`n", "`n") -cne $env:CREATOR_TEST_LICENSE.Replace("`r`n", "`n")) {
        throw 'About should show the embedded MIT license as read-only text'
    }
    Invoke-Control $about '返回'
    [void](Find-Control $about "版本 $version")
    Invoke-Control $about '复制版本号'
    [void](Find-Control $about '已复制')
    if ([System.Windows.Forms.Clipboard]::GetText() -cne $version) { throw 'About did not copy the installed version' }
    Send-WindowKeys $about '^w'
    Wait-WindowClosed '关于 Creator'
    Write-Output 'The Start menu restored the original Creator window and draft. The Creator menu, Ctrl+Comma, Escape and About worked.'
}

$process = Get-Process -Id $creatorPid
if (!$process.CloseMainWindow() -or !$process.WaitForExit(8000)) { throw 'Creator did not quit after a normal window close' }
