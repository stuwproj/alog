Feature: Scanning records
  A scanner reads the retained records in order, and can be positioned
  by sequence.

  Background:
    Given an empty log
    And the log contains "a", "b", "c", "d", "e"

  Scenario: A scan yields every record in order
    Then scanning the log yields "a", "b", "c", "d", "e"
    And the scanned sequences are 0, 1, 2, 3, 4

  Scenario: A scan can start at a sequence
    When I seek to sequence 2
    Then scanning yields "c", "d", "e"

  Scenario: A scan can end before a sequence
    When I seek to sequence 1 and stop at sequence 4
    Then scanning yields "b", "c", "d"

  Scenario: Seeking before the first record starts at the first record
    Given I truncate before sequence 2
    When I seek to sequence 0
    Then scanning yields "c", "d", "e"

  Scenario: Seeking past the end is out of bounds
    When I seek to sequence 5
    Then seeking fails with ErrOutOfBounds

  Scenario: A scanner follows the log
    Given a scanner that has read everything
    When I append "f"
    Then scanning yields "f"

  Scenario: A closed scanner no longer scans
    Given a scanner at the start
    When I close the scanner
    Then scanning fails with ErrClosed

  Scenario: Ranging over a window of the log
    When I range over sequences 1 to 3
    Then the range visited "b", "c"

  Scenario: Ranging to the end of the log
    When I range from sequence 3 to the end
    Then the range visited "d", "e"

  Scenario: A callback error stops a range
    When I range over the log and fail on "c"
    Then the range visited "a", "b", "c"
    And the range returned the callback error

  Scenario: A range ignores a start before the first record
    Given I truncate before sequence 3
    When I range from sequence 0 to the end
    Then the range visited "d", "e"

  Scenario: A limited scanner reads only the start of payloads
    Given the log contains "alpha", "beta"
    Then limited scanning from sequence 5 with a limit of 2 bytes yields "al", "be"
